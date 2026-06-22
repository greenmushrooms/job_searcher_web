// Package metrics computes per-user (per sys_profile) search analytics for the
// /monitor dashboard: the evaluate→present→review→apply funnel, a fit-score
// histogram, a salary histogram, and postings over time. Every query is scoped
// to one profile and an optional recency window (days; 0 = all time). The pure
// parsing/bucketing helpers are unit-tested; the SQL is thin aggregation.
package metrics

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/greenmushrooms/job_searcher_web/api/internal/chart"
	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

// Report is everything the monitor page renders, already bucketed.
type Report struct {
	Funnel      []chart.Stage // evaluated → presented → reviewed → applied
	Scores      []chart.Bar   // avg_score histogram (0–10)
	Salaries    []chart.Bar   // annualized-salary histogram
	SalaryWith  int           // jobs in window with a parseable salary
	SalaryTotal int           // jobs in window (coverage denominator)
	Postings    []chart.Bar   // count by posting month
}

// Load runs the per-profile aggregation for a recency window (days>0 restricts
// to evaluated_jobs.created_at within the last N days; 0 = all time).
func Load(ctx context.Context, q db.Querier, profile string, days int) (*Report, error) {
	where := "e.sys_profile = $1"
	args := []any{profile}
	if days > 0 {
		where += fmt.Sprintf(" AND e.created_at >= NOW() - ($%d || ' days')::interval", len(args)+1)
		args = append(args, strconv.Itoa(days))
	}

	// One row per job: the most recent evaluation within the window. A job
	// re-evaluated across runs (1,710 of ~13.5k for Slava) then counts once, and
	// the review join below can't multiply it.
	latest := `(SELECT DISTINCT ON (e.job_id) e.job_id, e.avg_score, e.notified_at
                FROM public.evaluated_jobs e WHERE ` + where + `
                ORDER BY e.job_id, e.created_at DESC) ej`

	rep := &Report{}
	var evaluated, presented, reviewed, applied int
	if err := q.QueryRow(ctx, `
        SELECT count(*),
               count(*) FILTER (WHERE ej.notified_at IS NOT NULL),
               count(*) FILTER (WHERE jr.job_id IS NOT NULL),
               count(*) FILTER (WHERE jr.status IN ('applied','screen','interview'))
        FROM `+latest+`
        LEFT JOIN web.job_review jr ON jr.job_id = ej.job_id AND jr.sys_profile = $1
    `, args...).Scan(&evaluated, &presented, &reviewed, &applied); err != nil {
		return nil, fmt.Errorf("funnel: %w", err)
	}
	rep.Funnel = []chart.Stage{
		{Label: "Evaluated", Count: evaluated},
		{Label: "Presented", Count: presented},
		{Label: "Reviewed", Count: reviewed},
		{Label: "Applied", Count: applied},
	}
	rep.SalaryTotal = evaluated

	// fit-score histogram: 10 buckets [0,1)…[9,10], width_bucket gives 1..10
	// (and 11 for exactly 10, folded back into 10).
	rep.Scores = make([]chart.Bar, 10)
	for i := range rep.Scores {
		rep.Scores[i] = chart.Bar{Label: strconv.Itoa(i), Value: 0}
	}
	rows, err := q.Query(ctx, `
        SELECT LEAST(width_bucket(ej.avg_score, 0, 10, 10), 10) AS b, count(*)
        FROM `+latest+` GROUP BY b ORDER BY b`, args...)
	if err != nil {
		return nil, fmt.Errorf("scores: %w", err)
	}
	for rows.Next() {
		var b, c int
		if err := rows.Scan(&b, &c); err != nil {
			rows.Close()
			return nil, err
		}
		if b >= 1 && b <= 10 {
			rep.Scores[b-1].Value = float64(c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// salary: parse the messy text amounts in Go, annualize, bucket.
	srows, err := q.Query(ctx, `
        SELECT j.min_amount, j.max_amount, j.interval
        FROM `+latest+` JOIN public.jobspy_jobs j ON j.id = ej.job_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("salary: %w", err)
	}
	var annuals []float64
	for srows.Next() {
		var mn, mx, iv *string
		if err := srows.Scan(&mn, &mx, &iv); err != nil {
			srows.Close()
			return nil, err
		}
		if a, ok := annualSalary(mn, mx, iv); ok {
			annuals = append(annuals, a)
		}
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return nil, err
	}
	rep.SalaryWith = len(annuals)
	rep.Salaries = salaryBuckets(annuals)

	// postings over time: count by posting month for the windowed set.
	prows, err := q.Query(ctx, `
        SELECT to_char(date_trunc('month', j.date_posted), 'Mon ''YY') AS m, count(*)
        FROM `+latest+` JOIN public.jobspy_jobs j ON j.id = ej.job_id
        WHERE j.date_posted IS NOT NULL
        GROUP BY date_trunc('month', j.date_posted)
        ORDER BY date_trunc('month', j.date_posted)`, args...)
	if err != nil {
		return nil, fmt.Errorf("postings: %w", err)
	}
	for prows.Next() {
		var label string
		var c int
		if err := prows.Scan(&label, &c); err != nil {
			prows.Close()
			return nil, err
		}
		rep.Postings = append(rep.Postings, chart.Bar{Label: label, Value: float64(c)})
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return nil, err
	}
	return rep, nil
}

// ── pure helpers (unit-tested) ───────────────────────────────────────────────

// annualSalary parses min/max amount text and an interval into a single annual
// figure (the midpoint when both bounds parse, else whichever does). Returns
// false when neither bound is a usable number.
func annualSalary(min, max, interval *string) (float64, bool) {
	lo, okLo := parseAmt(min)
	hi, okHi := parseAmt(max)
	var base float64
	switch {
	case okLo && okHi:
		base = (lo + hi) / 2
	case okLo:
		base = lo
	case okHi:
		base = hi
	default:
		return 0, false
	}
	a := annualize(base, interval)
	if a <= 0 {
		return 0, false
	}
	return a, true
}

// parseAmt parses a scraped amount string (plain number, or "120k"/"$120,000").
func parseAmt(s *string) (float64, bool) {
	if s == nil {
		return 0, false
	}
	t := strings.TrimSpace(*s)
	t = strings.NewReplacer("$", "", ",", "", " ", "").Replace(t)
	if t == "" {
		return 0, false
	}
	mult := 1.0
	if n := len(t); n > 0 && (t[n-1] == 'k' || t[n-1] == 'K') {
		mult, t = 1000, t[:n-1]
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v * mult, true
}

// annualize scales a per-interval figure to a yearly one. Unknown/empty
// intervals are treated as already-annual.
func annualize(v float64, interval *string) float64 {
	iv := ""
	if interval != nil {
		iv = strings.ToLower(strings.TrimSpace(*interval))
	}
	switch iv {
	case "hourly", "hour":
		return v * 2080
	case "daily", "day":
		return v * 260
	case "weekly", "week":
		return v * 52
	case "monthly", "month":
		return v * 12
	default: // yearly, annual, "", unknown
		return v
	}
}

// salaryRanges are the fixed annual-salary buckets, in thousands.
var salaryRanges = []struct {
	lo, hi float64 // hi=0 means open-ended
	label  string
}{
	{0, 50, "<50k"}, {50, 75, "50–75k"}, {75, 100, "75–100k"},
	{100, 125, "100–125k"}, {125, 150, "125–150k"}, {150, 200, "150–200k"},
	{200, 0, "200k+"},
}

// salaryBuckets buckets annual salaries (in dollars) into salaryRanges.
func salaryBuckets(annuals []float64) []chart.Bar {
	bars := make([]chart.Bar, len(salaryRanges))
	for i, r := range salaryRanges {
		bars[i].Label = r.label
	}
	for _, a := range annuals {
		k := a / 1000
		for i, r := range salaryRanges {
			if k >= r.lo && (r.hi == 0 || k < r.hi) {
				bars[i].Value++
				break
			}
		}
	}
	return bars
}
