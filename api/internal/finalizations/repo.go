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
	KeptBulletIDs []string        `json:"kept_bullet_ids"`
	Removals      json.RawMessage `json:"removals"`
	GeneratedAt   string          `json:"generated_at"`
}

type Repo struct {
	q db.Querier
}

func New(q db.Querier) *Repo { return &Repo{q: q} }

// Save upserts the per-job tailored resume and writes a resume_generated event
// in the same operation. removals is the LLM removal diff as jsonb bytes (the
// caller marshals it); nil is stored as an empty array.
func (r *Repo) Save(ctx context.Context, jobID, sysProfile, resumeVersion string, keptIDs []string, removals []byte) (*Finalization, error) {
	if jobID == "" || sysProfile == "" {
		return nil, errors.New("job_id and sys_profile required")
	}
	if keptIDs == nil {
		keptIDs = []string{}
	}
	if len(removals) == 0 {
		removals = []byte("[]")
	}

	var f Finalization
	err := r.q.QueryRow(ctx, `
        INSERT INTO web.jobs_resume
            (job_id, sys_profile, resume_version, kept_bullet_ids, removals, generated_at)
        VALUES ($1, $2, $3, $4, $5::jsonb, NOW())
        ON CONFLICT (job_id, sys_profile) DO UPDATE
        SET resume_version  = EXCLUDED.resume_version,
            kept_bullet_ids = EXCLUDED.kept_bullet_ids,
            removals        = EXCLUDED.removals,
            generated_at    = NOW()
        RETURNING job_id, sys_profile, resume_version, kept_bullet_ids, removals, generated_at::text
    `, jobID, sysProfile, resumeVersion, keptIDs, string(removals)).Scan(
		&f.JobID, &f.SysProfile, &f.ResumeVersion, &f.KeptBulletIDs, &f.Removals, &f.GeneratedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert jobs_resume: %w", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"resume_version":  resumeVersion,
		"kept_bullet_ids": keptIDs,
		"kept_count":      len(keptIDs),
	})
	if _, err := r.q.Exec(ctx, `
        INSERT INTO web.application_events (sys_profile, job_id, event_type, payload)
        VALUES ($1, $2, 'resume_generated', $3::jsonb)
    `, sysProfile, jobID, string(payload)); err != nil {
		return nil, fmt.Errorf("write event: %w", err)
	}
	return &f, nil
}

// Get returns the saved tailored resume for (jobID, sysProfile) or nil if none.
func (r *Repo) Get(ctx context.Context, jobID, sysProfile string) (*Finalization, error) {
	var f Finalization
	err := r.q.QueryRow(ctx, `
        SELECT job_id, sys_profile, resume_version, kept_bullet_ids, removals, generated_at::text
        FROM web.jobs_resume
        WHERE job_id = $1 AND sys_profile = $2
    `, jobID, sysProfile).Scan(
		&f.JobID, &f.SysProfile, &f.ResumeVersion, &f.KeptBulletIDs, &f.Removals, &f.GeneratedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}
