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

	var f Finalization
	err := r.q.QueryRow(ctx, `
        INSERT INTO web.jobs_resume
            (job_id, sys_profile, resume_version, template_id, kept_bullet_ids, removals, bullets, markdown, generated_at)
        VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, NOW())
        ON CONFLICT (job_id, sys_profile) DO UPDATE
        SET resume_version  = EXCLUDED.resume_version,
            template_id     = EXCLUDED.template_id,
            kept_bullet_ids = EXCLUDED.kept_bullet_ids,
            removals        = EXCLUDED.removals,
            bullets         = EXCLUDED.bullets,
            markdown        = EXCLUDED.markdown,
            generated_at    = NOW()
        RETURNING job_id, sys_profile, resume_version, template_id, kept_bullet_ids, removals, bullets, COALESCE(markdown, ''), generated_at::text
    `, in.JobID, in.SysProfile, in.ResumeVersion, templateID, keptIDs, string(removals), string(bullets), in.Markdown).Scan(
		&f.JobID, &f.SysProfile, &f.ResumeVersion, &f.TemplateID, &f.KeptBulletIDs, &f.Removals, &f.Bullets, &f.Markdown, &f.GeneratedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert jobs_resume: %w", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"resume_version":  in.ResumeVersion,
		"template_id":     templateID,
		"kept_bullet_ids": keptIDs,
		"kept_count":      len(keptIDs),
	})
	if _, err := r.q.Exec(ctx, `
        INSERT INTO web.application_events (sys_profile, job_id, event_type, payload)
        VALUES ($1, $2, 'resume_generated', $3::jsonb)
    `, in.SysProfile, in.JobID, string(payload)); err != nil {
		return nil, fmt.Errorf("write event: %w", err)
	}
	return &f, nil
}

// Get returns the saved tailored resume for (jobID, sysProfile) or nil if none.
func (r *Repo) Get(ctx context.Context, jobID, sysProfile string) (*Finalization, error) {
	var f Finalization
	err := r.q.QueryRow(ctx, `
        SELECT job_id, sys_profile, resume_version, template_id, kept_bullet_ids, removals, bullets, COALESCE(markdown, ''), generated_at::text
        FROM web.jobs_resume
        WHERE job_id = $1 AND sys_profile = $2
    `, jobID, sysProfile).Scan(
		&f.JobID, &f.SysProfile, &f.ResumeVersion, &f.TemplateID, &f.KeptBulletIDs, &f.Removals, &f.Bullets, &f.Markdown, &f.GeneratedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}
