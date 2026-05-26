package jobs

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

type Compensation struct {
	Min      *int64  `json:"min"`
	Max      *int64  `json:"max"`
	Interval *string `json:"interval"`
	Currency *string `json:"currency"`
}

type Application struct {
	Status    string  `json:"status"`
	Notes     *string `json:"notes"`
	UpdatedAt string  `json:"updated_at"`
}

type Job struct {
	ID           string         `json:"id"`
	Title        *string        `json:"title"`
	Company      *string        `json:"company"`
	Location     *string        `json:"location"`
	IsRemote     *bool          `json:"is_remote"`
	DatePosted   *string        `json:"date_posted"`
	URL          *string        `json:"url"`
	Description  *string        `json:"description"`
	Compensation *Compensation  `json:"compensation"`
	Score        *float64       `json:"score"`
	Reasoning    map[string]any `json:"reasoning"`
	EvalDate     *string        `json:"eval_date"`
	Profile      *string        `json:"profile"`
	Country      *string        `json:"country"`
	Application  *Application   `json:"application"`
}

type ListParams struct {
	Profile   string
	MinScore  float64
	Limit     int
	Offset    int
	From      string // YYYY-MM-DD, optional
	To        string // YYYY-MM-DD, optional
	DateField string // "eval" or "posted"
}

const baseSelect = `
SELECT
    j.id, j.title, j.company, j.location, j.is_remote,
    j.date_posted::text,
    COALESCE(j.job_url_direct, j.job_url) AS url,
    j.description,
    j.min_amount, j.max_amount, j.interval, j.currency,
    e.avg_score, e.reasoning, e.created_at::text AS eval_date,
    e.sys_profile, j.sys_country,
    a.status, a.notes, a.updated_at::text AS application_updated_at
FROM public.evaluated_jobs e
JOIN public.jobspy_jobs j ON e.job_id = j.id
LEFT JOIN web.applications a
       ON a.job_id = j.id AND a.sys_profile = e.sys_profile
`

type Repo struct {
	q db.Querier
}

func New(q db.Querier) *Repo { return &Repo{q: q} }

func (r *Repo) List(ctx context.Context, p ListParams) ([]Job, error) {
	args := []any{p.Profile, p.MinScore}
	where := []string{"e.sys_profile = $1", "e.avg_score >= $2"}

	dateCol := "e.created_at::date"
	if p.DateField == "posted" {
		dateCol = "j.date_posted"
	}
	if p.From != "" {
		args = append(args, p.From)
		where = append(where, dateCol+" >= $"+strconv.Itoa(len(args)))
	}
	if p.To != "" {
		args = append(args, p.To)
		where = append(where, dateCol+" <= $"+strconv.Itoa(len(args)))
	}

	args = append(args, p.Limit, p.Offset)
	limitIdx := strconv.Itoa(len(args) - 1)
	offsetIdx := strconv.Itoa(len(args))

	sql := baseSelect + " WHERE " + strings.Join(where, " AND ") + `
        ORDER BY e.avg_score DESC, e.created_at DESC
        LIMIT $` + limitIdx + ` OFFSET $` + offsetIdx
	return r.queryJobs(ctx, sql, args...)
}

func (r *Repo) Get(ctx context.Context, id string) (*Job, error) {
	sql := baseSelect + " WHERE j.id = $1 ORDER BY e.created_at DESC LIMIT 1"
	jobs, err := r.queryJobs(ctx, sql, id)
	if err != nil || len(jobs) == 0 {
		return nil, err
	}
	return &jobs[0], nil
}

func (r *Repo) Profiles(ctx context.Context) ([]string, error) {
	rows, err := r.q.Query(ctx, `
        SELECT sys_profile
        FROM public.evaluated_jobs
        WHERE sys_profile IS NOT NULL AND sys_profile <> ''
        GROUP BY 1
        ORDER BY MAX(created_at) DESC NULLS LAST
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) queryJobs(ctx context.Context, sql string, args ...any) ([]Job, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var (
			j        Job
			minAmt   *int64
			maxAmt   *int64
			interval *string
			currency *string
			reason   []byte
			score    *float64
			appStat  *string
			appNotes *string
			appUpd   *string
		)
		if err := rows.Scan(
			&j.ID, &j.Title, &j.Company, &j.Location, &j.IsRemote,
			&j.DatePosted, &j.URL, &j.Description,
			&minAmt, &maxAmt, &interval, &currency,
			&score, &reason, &j.EvalDate,
			&j.Profile, &j.Country,
			&appStat, &appNotes, &appUpd,
		); err != nil {
			return nil, err
		}
		j.Score = score
		if minAmt != nil && maxAmt != nil {
			j.Compensation = &Compensation{Min: minAmt, Max: maxAmt, Interval: interval, Currency: currency}
		}
		if len(reason) > 0 {
			_ = json.Unmarshal(reason, &j.Reasoning)
		}
		if appStat != nil {
			updatedAt := ""
			if appUpd != nil {
				updatedAt = *appUpd
			}
			j.Application = &Application{Status: *appStat, Notes: appNotes, UpdatedAt: updatedAt}
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
