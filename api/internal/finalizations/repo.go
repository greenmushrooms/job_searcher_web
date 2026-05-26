// Package finalizations owns web.resume_finalizations: the user's confirmed
// bullet selection for a tailored resume per (job_id, sys_profile).
// Each save also appends a resume_finalized event to web.application_events
// so the LLM's draft and the user's final pick can be cross-referenced later.
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
	JobID          string   `json:"job_id"`
	SysProfile     string   `json:"sys_profile"`
	ResumeVersion  string   `json:"resume_version"`
	KeptBulletIDs  []string `json:"kept_bullet_ids"`
	FinalizedAt    string   `json:"finalized_at"`
}

type Repo struct {
	q db.Querier
}

func New(q db.Querier) *Repo { return &Repo{q: q} }

// Save upserts the finalization and writes a resume_finalized event in the
// same logical operation. Caller's responsibility to wrap both in a tx if
// atomicity matters across rows; for a single-row save the pool is fine.
func (r *Repo) Save(ctx context.Context, jobID, sysProfile, resumeVersion string, keptIDs []string) (*Finalization, error) {
	if jobID == "" || sysProfile == "" {
		return nil, errors.New("job_id and sys_profile required")
	}
	if keptIDs == nil {
		keptIDs = []string{}
	}

	var f Finalization
	err := r.q.QueryRow(ctx, `
        INSERT INTO web.resume_finalizations
            (job_id, sys_profile, resume_version, kept_bullet_ids, finalized_at)
        VALUES ($1, $2, $3, $4, NOW())
        ON CONFLICT (job_id, sys_profile) DO UPDATE
        SET resume_version  = EXCLUDED.resume_version,
            kept_bullet_ids = EXCLUDED.kept_bullet_ids,
            finalized_at    = NOW()
        RETURNING job_id, sys_profile, resume_version, kept_bullet_ids, finalized_at::text
    `, jobID, sysProfile, resumeVersion, keptIDs).Scan(
		&f.JobID, &f.SysProfile, &f.ResumeVersion, &f.KeptBulletIDs, &f.FinalizedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert finalization: %w", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"resume_version":  resumeVersion,
		"kept_bullet_ids": keptIDs,
		"kept_count":      len(keptIDs),
	})
	if _, err := r.q.Exec(ctx, `
        INSERT INTO web.application_events (sys_profile, job_id, event_type, payload)
        VALUES ($1, $2, 'resume_finalized', $3::jsonb)
    `, sysProfile, jobID, string(payload)); err != nil {
		return nil, fmt.Errorf("write event: %w", err)
	}
	return &f, nil
}

// Get returns the saved finalization for (jobID, sysProfile) or nil if none.
func (r *Repo) Get(ctx context.Context, jobID, sysProfile string) (*Finalization, error) {
	var f Finalization
	err := r.q.QueryRow(ctx, `
        SELECT job_id, sys_profile, resume_version, kept_bullet_ids, finalized_at::text
        FROM web.resume_finalizations
        WHERE job_id = $1 AND sys_profile = $2
    `, jobID, sysProfile).Scan(
		&f.JobID, &f.SysProfile, &f.ResumeVersion, &f.KeptBulletIDs, &f.FinalizedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}
