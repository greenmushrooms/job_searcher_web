// Package challenges owns web.jobs_challenge: a per-job practice exercise,
// one row per (job_id, sys_profile), generated from the posting's stated
// technical requirements. Saves append a challenge_saved event to
// web.application_events for the audit trail.
//
// The reference solution lives in the same row but is served separately from
// the exercise files, so the UI can withhold it until asked.
package challenges

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
)

type Challenge struct {
	JobID         string                   `json:"job_id"`
	SysProfile    string                   `json:"sys_profile"`
	Title         string                   `json:"title"`
	Brief         string                   `json:"brief"`
	Skills        []string                 `json:"skills"`
	Minutes       int                      `json:"minutes"`
	Files         []deepseek.ChallengeFile `json:"files"`
	Solution      []deepseek.ChallengeFile `json:"solution"`
	Model         string                   `json:"model"`
	PromptVersion string                   `json:"prompt_version"`
	UpdatedAt     string                   `json:"updated_at"`
}

type Repo struct {
	q db.Querier
}

func New(q db.Querier) *Repo { return &Repo{q: q} }

// Save upserts the exercise and writes a challenge_saved event. Re-drafting a
// job's challenge replaces it wholesale — unlike a cover letter there is no
// hand-edited state worth merging.
func (r *Repo) Save(ctx context.Context, jobID, sysProfile string, ch *Challenge) (*Challenge, error) {
	if jobID == "" || sysProfile == "" {
		return nil, errors.New("job_id and sys_profile required")
	}
	if ch == nil {
		return nil, errors.New("nil challenge")
	}
	files, err := json.Marshal(ch.Files)
	if err != nil {
		return nil, fmt.Errorf("marshal files: %w", err)
	}
	solution, err := json.Marshal(ch.Solution)
	if err != nil {
		return nil, fmt.Errorf("marshal solution: %w", err)
	}
	skills := ch.Skills
	if skills == nil {
		skills = []string{}
	}

	var out Challenge
	var filesRaw, solutionRaw []byte
	err = r.q.QueryRow(ctx, `
        INSERT INTO web.jobs_challenge
            (job_id, sys_profile, title, brief, skills, minutes, files, solution,
             model, prompt_version, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
        ON CONFLICT (job_id, sys_profile) DO UPDATE
        SET title          = EXCLUDED.title,
            brief          = EXCLUDED.brief,
            skills         = EXCLUDED.skills,
            minutes        = EXCLUDED.minutes,
            files          = EXCLUDED.files,
            solution       = EXCLUDED.solution,
            model          = EXCLUDED.model,
            prompt_version = EXCLUDED.prompt_version,
            updated_at     = NOW()
        RETURNING job_id, sys_profile, title, brief, skills, minutes,
                  files, solution, model, prompt_version, updated_at::text
    `, jobID, sysProfile, ch.Title, ch.Brief, skills, ch.Minutes, files, solution,
		ch.Model, ch.PromptVersion).Scan(
		&out.JobID, &out.SysProfile, &out.Title, &out.Brief, &out.Skills, &out.Minutes,
		&filesRaw, &solutionRaw, &out.Model, &out.PromptVersion, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert jobs_challenge: %w", err)
	}
	if err := unmarshalFiles(filesRaw, solutionRaw, &out); err != nil {
		return nil, err
	}

	if err := db.WriteEvent(ctx, r.q, sysProfile, jobID, "challenge_saved", map[string]any{
		"title":          out.Title,
		"minutes":        out.Minutes,
		"skills":         out.Skills,
		"file_count":     len(out.Files),
		"prompt_version": out.PromptVersion,
	}); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get returns the saved challenge for (jobID, sysProfile) or nil if none.
func (r *Repo) Get(ctx context.Context, jobID, sysProfile string) (*Challenge, error) {
	var out Challenge
	var filesRaw, solutionRaw []byte
	err := r.q.QueryRow(ctx, `
        SELECT job_id, sys_profile, title, brief, skills, minutes,
               files, solution, model, prompt_version, updated_at::text
        FROM web.jobs_challenge
        WHERE job_id = $1 AND sys_profile = $2
    `, jobID, sysProfile).Scan(
		&out.JobID, &out.SysProfile, &out.Title, &out.Brief, &out.Skills, &out.Minutes,
		&filesRaw, &solutionRaw, &out.Model, &out.PromptVersion, &out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := unmarshalFiles(filesRaw, solutionRaw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func unmarshalFiles(filesRaw, solutionRaw []byte, out *Challenge) error {
	if len(filesRaw) > 0 {
		if err := json.Unmarshal(filesRaw, &out.Files); err != nil {
			return fmt.Errorf("decode files: %w", err)
		}
	}
	if len(solutionRaw) > 0 {
		if err := json.Unmarshal(solutionRaw, &out.Solution); err != nil {
			return fmt.Errorf("decode solution: %w", err)
		}
	}
	return nil
}
