// Package coverletters owns web.jobs_cover_letter: the per-job cover letter,
// one row per (job_id, sys_profile). The body is free-form text — usually an
// AI first draft the user then edits. Saves append a cover_letter_saved event
// to web.application_events for the audit trail.
package coverletters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

type CoverLetter struct {
	JobID      string `json:"job_id"`
	SysProfile string `json:"sys_profile"`
	Body       string `json:"body"`
	Model      string `json:"model"` // LLM that produced the last AI draft, "" if hand-written
	UpdatedAt  string `json:"updated_at"`
}

type Repo struct {
	q db.Querier
}

func New(q db.Querier) *Repo { return &Repo{q: q} }

// Save upserts the cover letter and writes a cover_letter_saved event. An
// empty model preserves the previously stored one, so a manual edit of an AI
// draft keeps its provenance.
func (r *Repo) Save(ctx context.Context, jobID, sysProfile, body, model string) (*CoverLetter, error) {
	if jobID == "" || sysProfile == "" {
		return nil, errors.New("job_id and sys_profile required")
	}
	var cl CoverLetter
	err := r.q.QueryRow(ctx, `
        INSERT INTO web.jobs_cover_letter (job_id, sys_profile, body, model, updated_at)
        VALUES ($1, $2, $3, $4, NOW())
        ON CONFLICT (job_id, sys_profile) DO UPDATE
        SET body       = EXCLUDED.body,
            model      = CASE WHEN EXCLUDED.model <> '' THEN EXCLUDED.model
                              ELSE web.jobs_cover_letter.model END,
            updated_at = NOW()
        RETURNING job_id, sys_profile, body, model, updated_at::text
    `, jobID, sysProfile, body, model).Scan(
		&cl.JobID, &cl.SysProfile, &cl.Body, &cl.Model, &cl.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert jobs_cover_letter: %w", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"model":      cl.Model,
		"body_chars": len(body),
	})
	if _, err := r.q.Exec(ctx, `
        INSERT INTO web.application_events (sys_profile, job_id, event_type, payload)
        VALUES ($1, $2, 'cover_letter_saved', $3::jsonb)
    `, sysProfile, jobID, string(payload)); err != nil {
		return nil, fmt.Errorf("write event: %w", err)
	}
	return &cl, nil
}

// Get returns the saved cover letter for (jobID, sysProfile) or nil if none.
func (r *Repo) Get(ctx context.Context, jobID, sysProfile string) (*CoverLetter, error) {
	var cl CoverLetter
	err := r.q.QueryRow(ctx, `
        SELECT job_id, sys_profile, body, model, updated_at::text
        FROM web.jobs_cover_letter
        WHERE job_id = $1 AND sys_profile = $2
    `, jobID, sysProfile).Scan(
		&cl.JobID, &cl.SysProfile, &cl.Body, &cl.Model, &cl.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cl, nil
}
