package applications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

// ValidStatuses mirrors the CHECK constraint on web.job_review.status. Kept
// here so handlers can reject early before hitting the DB.
var ValidStatuses = map[string]bool{
	"applied":   true,
	"skipped":   true,
	"interview": true,
}

var (
	ErrInvalidStatus = errors.New("invalid status")
	ErrJobNotFound   = errors.New("job not found")
)

type Application struct {
	JobID      string  `json:"job_id"`
	SysProfile string  `json:"sys_profile"`
	Status     string  `json:"status"`
	Notes      *string `json:"notes"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

type Repo struct {
	q db.Querier
}

func New(q db.Querier) *Repo { return &Repo{q: q} }

// Upsert writes the (job_id, sys_profile, status, notes) row and appends a
// status_changed event in the same logical operation. The caller passes a
// Querier — typically a pool in prod, a pgx.Tx in tests.
//
// Returns ErrJobNotFound if job_id is not in public.jobspy_jobs; the FK-by-hand
// check is deliberate so we can return a clean 404 rather than a constraint
// error (the table has no real FK on job_id since jobspy_jobs may be repopulated).
func (r *Repo) Upsert(ctx context.Context, jobID, sysProfile, status string, notes *string) (*Application, error) {
	if !ValidStatuses[status] {
		return nil, fmt.Errorf("%w: %q", ErrInvalidStatus, status)
	}

	var exists bool
	err := r.q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM public.jobspy_jobs WHERE id = $1)`,
		jobID,
	).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("check job exists: %w", err)
	}
	if !exists {
		return nil, ErrJobNotFound
	}

	var app Application
	err = r.q.QueryRow(ctx, `
        INSERT INTO web.job_review (job_id, sys_profile, status, notes, created_at, updated_at)
        VALUES ($1, $2, $3, $4, NOW(), NOW())
        ON CONFLICT (job_id, sys_profile) DO UPDATE
        SET status     = EXCLUDED.status,
            notes      = EXCLUDED.notes,
            updated_at = NOW()
        RETURNING job_id, sys_profile, status, notes,
                  created_at::text, updated_at::text
    `, jobID, sysProfile, status, notes).Scan(
		&app.JobID, &app.SysProfile, &app.Status, &app.Notes,
		&app.CreatedAt, &app.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert application: %w", err)
	}

	payload, _ := json.Marshal(map[string]any{"status": status, "notes": notes})
	if _, err := r.q.Exec(ctx, `
        INSERT INTO web.application_events (sys_profile, job_id, event_type, payload)
        VALUES ($1, $2, 'status_changed', $3::jsonb)
    `, sysProfile, jobID, string(payload)); err != nil {
		return nil, fmt.Errorf("write event: %w", err)
	}

	return &app, nil
}

// Get returns the current application row for (jobID, sysProfile), or nil if none.
func (r *Repo) Get(ctx context.Context, jobID, sysProfile string) (*Application, error) {
	var app Application
	err := r.q.QueryRow(ctx, `
        SELECT job_id, sys_profile, status, notes, created_at::text, updated_at::text
        FROM web.job_review
        WHERE job_id = $1 AND sys_profile = $2
    `, jobID, sysProfile).Scan(
		&app.JobID, &app.SysProfile, &app.Status, &app.Notes,
		&app.CreatedAt, &app.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &app, nil
}
