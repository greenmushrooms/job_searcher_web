// Package finalizations owns web.jobs_resume: the per-job tailored resume the
// user generated, one row per (job_id, sys_profile). It stores the final
// bullet selection plus a snapshot of the LLM's removal diff, and appends a
// resume_generated event to web.application_events for the audit trail.
package finalizations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

type Finalization struct {
	JobID         string          `json:"job_id"`
	SysProfile    string          `json:"sys_profile"`
	ResumeVersion string          `json:"resume_version"`
	TemplateID    string          `json:"template_id"`
	KeptBulletIDs []string        `json:"kept_bullet_ids"`
	Removals      json.RawMessage `json:"removals"`
	// Bullets is the per-bullet final snapshot ([{role_id,bullet_id,text,
	// source}]) — the rendered resume's actual text, including manual edits and
	// accepted AI rewrites. The PDF renderer reads this rather than re-deriving
	// from canonical.
	Bullets     json.RawMessage `json:"bullets"`
	// Markdown is the finalized resume as a free-form markdown document — the
	// source of truth the PDF is rendered from in the markdown-centric flow.
	Markdown    string `json:"markdown"`
	GeneratedAt string `json:"generated_at"`
	// SCD Type 2 version metadata. Version increments per (job_id, sys_profile);
	// exactly one row per key has IsCurrent=true. ValidTo is the supersede time
	// of an expired version ("" while current).
	Version   int    `json:"version"`
	IsCurrent bool   `json:"is_current"`
	ValidTo   string `json:"valid_to,omitempty"`
}

// finalizationCols is the shared projection for every Finalization read, in the
// order scanFinalization expects.
const finalizationCols = `job_id, sys_profile, resume_version, template_id,
    kept_bullet_ids, removals, bullets, COALESCE(markdown, ''),
    generated_at::text, version, is_current, COALESCE(valid_to::text, '')`

// scannable is satisfied by both pgx.Row (QueryRow) and pgx.Rows (Query).
type scannable interface{ Scan(dest ...any) error }

func scanFinalization(s scannable) (*Finalization, error) {
	var f Finalization
	if err := s.Scan(
		&f.JobID, &f.SysProfile, &f.ResumeVersion, &f.TemplateID,
		&f.KeptBulletIDs, &f.Removals, &f.Bullets, &f.Markdown,
		&f.GeneratedAt, &f.Version, &f.IsCurrent, &f.ValidTo,
	); err != nil {
		return nil, err
	}
	return &f, nil
}

// SaveInput is the per-job tailored-resume write. Removals and Bullets are
// pre-marshalled jsonb bytes; nil/empty become empty arrays.
type SaveInput struct {
	JobID         string
	SysProfile    string
	ResumeVersion string
	TemplateID    string
	KeptBulletIDs []string
	Removals      []byte
	Bullets       []byte
	Markdown      string
}

type Repo struct {
	q db.Querier
}

func New(q db.Querier) *Repo { return &Repo{q: q} }

// nextVersionExpr computes the next version number for ($1 job_id, $2 sys_profile).
// It reads MAX over all versions (current or expired), so it is unaffected by an
// earlier same-tx expire of the current row (expiring doesn't change version).
const nextVersionExpr = `COALESCE((SELECT MAX(version) FROM web.jobs_resume WHERE job_id = $1 AND sys_profile = $2), 0) + 1`

// txBeginner is satisfied by both *pgxpool.Pool and pgx.Tx, so Save/Restore can
// run their expire-then-insert as a real transaction whether the repo was given
// the pool (production) or a tx (tests, where Begin opens a nested savepoint).
type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

func (r *Repo) begin(ctx context.Context) (pgx.Tx, error) {
	b, ok := r.q.(txBeginner)
	if !ok {
		return nil, errors.New("finalizations: querier does not support transactions")
	}
	return b.Begin(ctx)
}

// expireCurrent closes the current version (if any) for the key, within tx. This
// MUST run before inserting the new current row in the same tx: the partial
// unique index jobs_resume_current_uk allows only one is_current row per key,
// and a single-statement expire+insert would trip it (CTEs share one snapshot).
func expireCurrent(ctx context.Context, tx pgx.Tx, jobID, sysProfile string) error {
	_, err := tx.Exec(ctx, `
        UPDATE web.jobs_resume SET is_current = false, valid_to = NOW()
         WHERE job_id = $1 AND sys_profile = $2 AND is_current
    `, jobID, sysProfile)
	return err
}

// Save upserts the per-job tailored resume and writes a resume_generated event
// in the same operation.
func (r *Repo) Save(ctx context.Context, in SaveInput) (*Finalization, error) {
	if in.JobID == "" || in.SysProfile == "" {
		return nil, errors.New("job_id and sys_profile required")
	}
	keptIDs := in.KeptBulletIDs
	if keptIDs == nil {
		keptIDs = []string{}
	}
	removals := in.Removals
	if len(removals) == 0 {
		removals = []byte("[]")
	}
	bullets := in.Bullets
	if len(bullets) == 0 {
		bullets = []byte("[]")
	}
	templateID := in.TemplateID
	if templateID == "" {
		templateID = "default"
	}

	// SCD Type 2: in one transaction, expire the current version (keep the row)
	// and insert a new current version with the next number.
	tx, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := expireCurrent(ctx, tx, in.JobID, in.SysProfile); err != nil {
		return nil, fmt.Errorf("expire current: %w", err)
	}
	f, err := scanFinalization(tx.QueryRow(ctx, `
        INSERT INTO web.jobs_resume
            (job_id, sys_profile, resume_version, template_id, kept_bullet_ids, removals, bullets, markdown, version, is_current, generated_at)
        SELECT $1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, `+nextVersionExpr+`, true, NOW()
        RETURNING `+finalizationCols+`
    `, in.JobID, in.SysProfile, in.ResumeVersion, templateID, keptIDs, string(removals), string(bullets), in.Markdown))
	if err != nil {
		return nil, fmt.Errorf("insert jobs_resume version: %w", err)
	}

	if err := writeGeneratedEvent(ctx, tx, in.SysProfile, in.JobID, in.ResumeVersion, templateID, keptIDs); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return f, nil
}

// writeGeneratedEvent appends the resume_generated audit event shared by Save
// and Restore (a restore produces a new current version, same as a save). q is
// the enclosing transaction so the event commits atomically with the version.
func writeGeneratedEvent(ctx context.Context, q db.Querier, sysProfile, jobID, resumeVersion, templateID string, keptIDs []string) error {
	payload, _ := json.Marshal(map[string]any{
		"resume_version":  resumeVersion,
		"template_id":     templateID,
		"kept_bullet_ids": keptIDs,
		"kept_count":      len(keptIDs),
	})
	if _, err := q.Exec(ctx, `
        INSERT INTO web.application_events (sys_profile, job_id, event_type, payload)
        VALUES ($1, $2, 'resume_generated', $3::jsonb)
    `, sysProfile, jobID, string(payload)); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	return nil
}

// Get returns the current tailored resume for (jobID, sysProfile) or nil if none.
func (r *Repo) Get(ctx context.Context, jobID, sysProfile string) (*Finalization, error) {
	f, err := scanFinalization(r.q.QueryRow(ctx, `
        SELECT `+finalizationCols+`
        FROM web.jobs_resume
        WHERE job_id = $1 AND sys_profile = $2 AND is_current
    `, jobID, sysProfile))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

// History returns every saved version for (jobID, sysProfile), newest first.
func (r *Repo) History(ctx context.Context, jobID, sysProfile string) ([]*Finalization, error) {
	rows, err := r.q.Query(ctx, `
        SELECT `+finalizationCols+`
        FROM web.jobs_resume
        WHERE job_id = $1 AND sys_profile = $2
        ORDER BY version DESC
    `, jobID, sysProfile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Finalization
	for rows.Next() {
		f, err := scanFinalization(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Restore makes a past version current again by copying its content into a new
// current version (the existing current is expired, never overwritten). Returns
// the newly-created current version, or an error if `version` does not exist.
func (r *Repo) Restore(ctx context.Context, jobID, sysProfile string, version int) (*Finalization, error) {
	if jobID == "" || sysProfile == "" {
		return nil, errors.New("job_id and sys_profile required")
	}
	tx, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Guard first so we only expire the current version when the source exists;
	// a bad version leaves the current row untouched.
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM web.jobs_resume WHERE job_id = $1 AND sys_profile = $2 AND version = $3)`,
		jobID, sysProfile, version,
	).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("version %d not found for job %s", version, jobID)
	}

	if err := expireCurrent(ctx, tx, jobID, sysProfile); err != nil {
		return nil, fmt.Errorf("expire current: %w", err)
	}
	f, err := scanFinalization(tx.QueryRow(ctx, `
        INSERT INTO web.jobs_resume
            (job_id, sys_profile, resume_version, template_id, kept_bullet_ids, removals, bullets, markdown, version, is_current, generated_at)
        SELECT job_id, sys_profile, resume_version, template_id, kept_bullet_ids, removals, bullets, markdown, `+nextVersionExpr+`, true, NOW()
          FROM web.jobs_resume
         WHERE job_id = $1 AND sys_profile = $2 AND version = $3
        RETURNING `+finalizationCols+`
    `, jobID, sysProfile, version))
	if err != nil {
		return nil, fmt.Errorf("restore jobs_resume version: %w", err)
	}

	if err := writeGeneratedEvent(ctx, tx, f.SysProfile, f.JobID, f.ResumeVersion, f.TemplateID, f.KeptBulletIDs); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return f, nil
}
