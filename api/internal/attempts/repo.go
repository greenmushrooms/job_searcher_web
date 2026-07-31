package attempts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

// Attempt is one stored practice run log. challenge_title and skills are
// denormalised on purpose: re-rolling a job's exercise must not rewrite the
// history of what you already sat down and did.
type Attempt struct {
	ID             int64      `json:"id"`
	JobID          string     `json:"job_id"`
	SysProfile     string     `json:"sys_profile"`
	ChallengeTitle string     `json:"challenge_title"`
	Skills         []string   `json:"skills"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	Runs           int        `json:"runs"`
	TotalTests     int        `json:"total_tests"`
	FirstPass      int        `json:"first_pass"`
	FinalPass      int        `json:"final_pass"`
	Solved         bool       `json:"solved"`
	BugGreenRun    int        `json:"bug_green_run"`
	OtherGreenRun  int        `json:"other_green_run"`
	BugFirst       *bool      `json:"bug_first"`
	RevealedSoln   bool       `json:"revealed_solution"`
	CreatedAt      string     `json:"created_at"`
	MinutesToGreen float64    `json:"minutes_to_green"`
}

type Repo struct{ q db.Querier }

func New(q db.Querier) *Repo { return &Repo{q: q} }

// Save records a scored attempt. Re-uploading the same log is a no-op rather
// than a duplicate: (job, profile, started_at) is the natural key, and the log
// is append-only, so the later upload of a longer session legitimately updates
// the same row with the extra runs.
func (r *Repo) Save(ctx context.Context, jobID, sysProfile, title string, skills []string,
	s *Scored, revealed bool) (*Attempt, error) {

	if jobID == "" || sysProfile == "" {
		return nil, errors.New("job_id and sys_profile required")
	}
	if s == nil {
		return nil, errors.New("nil score")
	}
	detail, err := json.Marshal(s.Detail)
	if err != nil {
		return nil, fmt.Errorf("marshal runs: %w", err)
	}
	if skills == nil {
		skills = []string{}
	}

	var a Attempt
	err = r.q.QueryRow(ctx, `
        INSERT INTO web.challenge_attempts
            (job_id, sys_profile, challenge_title, skills, started_at, finished_at,
             runs, total_tests, first_pass, final_pass, solved,
             bug_green_run, other_green_run, bug_first, revealed_solution, runs_detail)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
        ON CONFLICT (job_id, sys_profile, started_at) DO UPDATE
        SET finished_at       = EXCLUDED.finished_at,
            runs              = EXCLUDED.runs,
            total_tests       = EXCLUDED.total_tests,
            final_pass        = EXCLUDED.final_pass,
            solved            = EXCLUDED.solved,
            bug_green_run     = EXCLUDED.bug_green_run,
            other_green_run   = EXCLUDED.other_green_run,
            bug_first         = EXCLUDED.bug_first,
            revealed_solution = web.challenge_attempts.revealed_solution OR EXCLUDED.revealed_solution,
            runs_detail       = EXCLUDED.runs_detail
        WHERE EXCLUDED.runs >= web.challenge_attempts.runs
        RETURNING id, job_id, sys_profile, challenge_title, skills, started_at,
                  finished_at, runs, total_tests, first_pass, final_pass, solved,
                  bug_green_run, other_green_run, bug_first, revealed_solution,
                  created_at::text
    `, jobID, sysProfile, title, skills, s.StartedAt, s.FinishedAt,
		s.Runs, s.TotalTests, s.FirstPass, s.FinalPass, s.Solved,
		s.BugGreenRun, s.OtherGreenRun, s.BugFirst, revealed, detail).Scan(
		&a.ID, &a.JobID, &a.SysProfile, &a.ChallengeTitle, &a.Skills, &a.StartedAt,
		&a.FinishedAt, &a.Runs, &a.TotalTests, &a.FirstPass, &a.FinalPass, &a.Solved,
		&a.BugGreenRun, &a.OtherGreenRun, &a.BugFirst, &a.RevealedSoln, &a.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert challenge_attempt: %w", err)
	}
	a.MinutesToGreen = minutesBetween(a.StartedAt, a.FinishedAt)
	return &a, nil
}

// ForJob lists attempts on one job, newest first.
func (r *Repo) ForJob(ctx context.Context, jobID, sysProfile string) ([]Attempt, error) {
	rows, err := r.q.Query(ctx, `
        SELECT id, job_id, sys_profile, challenge_title, skills, started_at,
               finished_at, runs, total_tests, first_pass, final_pass, solved,
               bug_green_run, other_green_run, bug_first, revealed_solution,
               created_at::text
        FROM web.challenge_attempts
        WHERE job_id = $1 AND sys_profile = $2
        ORDER BY started_at DESC
    `, jobID, sysProfile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Attempt
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.ID, &a.JobID, &a.SysProfile, &a.ChallengeTitle, &a.Skills,
			&a.StartedAt, &a.FinishedAt, &a.Runs, &a.TotalTests, &a.FirstPass, &a.FinalPass,
			&a.Solved, &a.BugGreenRun, &a.OtherGreenRun, &a.BugFirst, &a.RevealedSoln,
			&a.CreatedAt); err != nil {
			return nil, err
		}
		a.MinutesToGreen = minutesBetween(a.StartedAt, a.FinishedAt)
		out = append(out, a)
	}
	return out, rows.Err()
}

// Summary is the practice log rolled up for one profile. Counts, not rates:
// with a handful of attempts a percentage reads as insight it hasn't earned.
type Summary struct {
	Attempts    int
	Solved      int
	BugFirst    int // localised the planted fault before the obvious stub work
	BugLater    int
	Revealed    int
	MedianMins  float64
	SkillCounts map[string]int
	SkillSolved map[string]int
}

// SummaryFor rolls up every attempt for a profile.
func (r *Repo) SummaryFor(ctx context.Context, sysProfile string) (*Summary, error) {
	rows, err := r.q.Query(ctx, `
        SELECT skills, started_at, finished_at, solved, bug_first, revealed_solution
        FROM web.challenge_attempts WHERE sys_profile = $1
    `, sysProfile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	s := &Summary{SkillCounts: map[string]int{}, SkillSolved: map[string]int{}}
	var mins []float64
	for rows.Next() {
		var (
			skills   []string
			started  time.Time
			finished *time.Time
			solved   bool
			bugFirst *bool
			revealed bool
		)
		if err := rows.Scan(&skills, &started, &finished, &solved, &bugFirst, &revealed); err != nil {
			return nil, err
		}
		s.Attempts++
		if solved {
			s.Solved++
			if m := minutesBetween(started, finished); m > 0 {
				mins = append(mins, m)
			}
		}
		if revealed {
			s.Revealed++
		}
		if bugFirst != nil {
			if *bugFirst {
				s.BugFirst++
			} else {
				s.BugLater++
			}
		}
		for _, sk := range skills {
			s.SkillCounts[sk]++
			if solved {
				s.SkillSolved[sk]++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.MedianMins = median(mins)
	return s, nil
}

func minutesBetween(start time.Time, end *time.Time) float64 {
	if end == nil {
		return 0
	}
	return end.Sub(start).Minutes()
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]float64{}, xs...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
