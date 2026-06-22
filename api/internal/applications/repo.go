package applications

import (
	"context"
	"errors"
	"fmt"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

// Review state has two axes (see migration 014). ValidStages is the active
// pipeline half of web.job_review.status; ValidFinalStatuses is the terminal
// outcome half (web.job_review.final_status). Upsert accepts a value from either
// set and routes it to the right column. Kept here so handlers can reject early
// before hitting the DB.
var ValidStages = map[string]bool{
	"applied":   true,
	"skipped":   true,
	"screen":    true,
	"interview": true,
}

var ValidFinalStatuses = map[string]bool{
	"rejected": true,
	"offer":    true,
}

// ValidStatuses is every value Upsert accepts (stages ∪ outcomes). Handlers use
// it to enumerate the vocabulary in error responses.
var ValidStatuses = map[string]bool{
	"applied": true, "skipped": true, "screen": true, "interview": true,
	"rejected": true, "offer": true,
}

var (
	ErrInvalidStatus = errors.New("invalid status")
	ErrJobNotFound   = errors.New("job not found")
)

type Application struct {
	JobID       string  `json:"job_id"`
	SysProfile  string  `json:"sys_profile"`
	Status      string  `json:"status"`
	FinalStatus *string `json:"final_status"` // nil = still open
	FinalAt     *string `json:"final_at"`
	Notes       *string `json:"notes"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type Repo struct {
	q db.Querier
}

func New(q db.Querier) *Repo { return &Repo{q: q} }

// Upsert records a review transition for (jobID, sysProfile). The value is
// either an active stage (applied/screen/interview/skipped → job_review.status)
// or a terminal outcome (rejected/offer → final_status + final_at); it's routed
// to the right column without clobbering the other axis. A typed event
// (event_type = value) is appended in the same logical operation so the funnel
// is reconstructable from web.application_events. The caller passes a Querier —
// typically a pool in prod, a pgx.Tx in tests.
//
// Returns ErrJobNotFound if job_id is not in public.jobspy_jobs; the FK-by-hand
// check is deliberate so we can return a clean 404 rather than a constraint
// error (the table has no real FK on job_id since jobspy_jobs may be repopulated).
func (r *Repo) Upsert(ctx context.Context, jobID, sysProfile, value string, notes *string) (*Application, error) {
	isStage := ValidStages[value]
	isFinal := ValidFinalStatuses[value]
	if !isStage && !isFinal {
		return nil, fmt.Errorf("%w: %q", ErrInvalidStatus, value)
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

	// A stage sets the active stage; an outcome sets final_status/final_at and
	// leaves status alone (defaulting to 'applied' if we'd otherwise insert a
	// row with no stage — you can't be rejected from a job you never applied to).
	// COALESCE keeps an existing note when the transition carries none, so a
	// status click doesn't wipe notes.
	sql := `
        INSERT INTO web.job_review (job_id, sys_profile, status, notes, created_at, updated_at)
        VALUES ($1, $2, $3, $4, NOW(), NOW())
        ON CONFLICT (job_id, sys_profile) DO UPDATE
        SET status     = EXCLUDED.status,
            notes      = COALESCE(EXCLUDED.notes, web.job_review.notes),
            updated_at = NOW()
        RETURNING job_id, sys_profile, status, final_status, final_at::text,
                  notes, created_at::text, updated_at::text`
	if isFinal {
		sql = `
        INSERT INTO web.job_review (job_id, sys_profile, status, final_status, final_at, notes, created_at, updated_at)
        VALUES ($1, $2, 'applied', $3, NOW(), $4, NOW(), NOW())
        ON CONFLICT (job_id, sys_profile) DO UPDATE
        SET final_status = EXCLUDED.final_status,
            final_at     = NOW(),
            notes        = COALESCE(EXCLUDED.notes, web.job_review.notes),
            updated_at   = NOW()
        RETURNING job_id, sys_profile, status, final_status, final_at::text,
                  notes, created_at::text, updated_at::text`
	}

	var app Application
	if err = r.q.QueryRow(ctx, sql, jobID, sysProfile, value, notes).Scan(
		&app.JobID, &app.SysProfile, &app.Status, &app.FinalStatus, &app.FinalAt,
		&app.Notes, &app.CreatedAt, &app.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("upsert application: %w", err)
	}

	if err := db.WriteEvent(ctx, r.q, sysProfile, jobID, value, map[string]any{"value": value, "notes": notes}); err != nil {
		return nil, err
	}

	return &app, nil
}
