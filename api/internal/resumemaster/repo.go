// Package resumemaster owns web.resume_master: the per-profile master résumé as
// a free-form markdown document. It's the "original" the diff-lab left pane
// edits and persists permanently, separate from the per-job tailored copy in
// web.jobs_resume.
package resumemaster

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

type Repo struct {
	q db.Querier
}

func New(q db.Querier) *Repo { return &Repo{q: q} }

// Get returns the stored master markdown for a profile, or "" if none is saved.
func (r *Repo) Get(ctx context.Context, profile string) (string, error) {
	var md string
	err := r.q.QueryRow(ctx,
		`SELECT markdown FROM web.resume_master WHERE sys_profile = $1`, profile,
	).Scan(&md)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return md, nil
}

// Save upserts the master markdown for a profile.
func (r *Repo) Save(ctx context.Context, profile, markdown string) error {
	if profile == "" {
		return errors.New("profile required")
	}
	_, err := r.q.Exec(ctx, `
        INSERT INTO web.resume_master (sys_profile, markdown, updated_at)
        VALUES ($1, $2, NOW())
        ON CONFLICT (sys_profile) DO UPDATE
        SET markdown = EXCLUDED.markdown, updated_at = NOW()
    `, profile, markdown)
	return err
}
