// Package stats aggregates a profile's pipeline history for the /stats page:
// how many jobs were scraped and evaluated, how many cleared the match bar,
// what the matched salaries look like, and where the matches cluster.
//
// Counts are per distinct job (a job re-evaluated in two runs counts once,
// at its best score) so the page answers "how many jobs" rather than "how
// many evaluation rows".
package stats

import (
	"context"
	"math"
	"sort"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

type Repo struct {
	q db.Querier
}

func New(q db.Querier) *Repo { return &Repo{q: q} }

// Summary is the all-time headline for one profile.
type Summary struct {
	Scraped   int // rows in jobspy_jobs for this profile
	Evaluated int // distinct jobs evaluated
	Matches   int // distinct jobs at/above the threshold
	AvgScore  float64
	Reviewed  int // jobs with any review decision (web.job_review)
	Applied   int // applied/screen/interview (skipped excluded)
	Interview int // reached screen or interview
	Offers    int
	Rejected  int
}

// WeekRow is one week of eval volume: everything scored vs. matches.
type WeekRow struct {
	Week    string // YYYY-MM-DD (week start)
	Evals   int
	Matches int
}

// Company is a repeat matcher: a company with >= 2 distinct matched jobs.
// Title buckets reuse the shape; for those, Key is the normalized family key
// the filter matches on ("data engineer"), Name is the display form, and
// Senior counts the bucket's postings whose original title carried a
// senior-tier marker (senior / sr / staff / principal / lead).
type Company struct {
	Key      string
	Name     string
	N        int
	Senior   int
	AvgScore float64
}

// Verdict is the model's read on a match (Lateral / Step up / ...).
type Verdict struct {
	Name string
	N    int
}

// TitleToken is a frequent word across matched job titles.
type TitleToken struct {
	Token string
	N     int
}

// SalaryMid is one matched posting's yearly-normalized comp midpoint, tagged
// with whether its title was senior-tier (senior/sr/staff/principal/lead).
type SalaryMid struct {
	Mid    int
	Senior bool
}

// Salaries summarizes yearly-normalized midpoints of matched jobs that carry
// a parseable compensation range.
type Salaries struct {
	N        int // matches with usable comp
	P25      int
	Median   int
	P75      int
	Min, Max int
	Mids     []SalaryMid // sorted by Mid, for binning in the handler
}

const thresholdDefault = 6.9

// GhostAfterDays: an application still sitting at 'applied' with no outcome
// after this many days counts as ghosted rather than awaiting a reply.
const GhostAfterDays = 21

// Funnel tracks submitted applications (skipped rows excluded) through stages
// and outcomes. Stage counts are cumulative — an application at 'interview'
// also counts in Screen — because job_review.status records the furthest stage
// reached and outcomes land in final_status without rewinding it. The five
// outcome buckets partition Applied.
type Funnel struct {
	Applied   int // applications submitted (status applied/screen/interview)
	Screen    int // reached screen or further
	Interview int // reached interview
	Offers    int // final_status = offer
	Rejected  int // final_status = rejected
	InProcess int // at screen/interview, no outcome yet
	Waiting   int // still 'applied', no outcome, within the ghost window
	Ghosted   int // still 'applied', no outcome, GhostAfterDays+ old
}

// normTitleExpr buckets a posting title into its role family so seniority
// variants aggregate instead of splintering: strip Indeed's fused "…New"
// badge, lowercase, punctuation → spaces, drop seniority/level tokens
// (senior, sr, lead, staff, II, numerals, …), collapse whitespace. "Lead Data
// Engineer", "Sr. Data Engineer II" and "Data Engineer" all key to
// "data engineer". Needs jobspy_jobs alias j in scope.
const normTitleExpr = `btrim(regexp_replace(regexp_replace(regexp_replace(` +
	`lower(regexp_replace(j.title, '([a-z])New$', '\1')),` +
	` '[^[:alnum:]]+', ' ', 'g'),` +
	` '\m(senior|sr|junior|jr|lead|staff|principal|intermediate|associate|ii|iii|iv|[0-9]+)\M', ' ', 'g'),` +
	` '\s+', ' ', 'g'))`

// titleClause narrows "matches" to one title family when $3 is set ("" = all).
// $3 carries the normalized family key from the chips.
const titleClause = ` AND ($3 = '' OR ` + normTitleExpr + ` = lower(btrim($3)))`

// Overview holds everything the stats page renders.
type Overview struct {
	Profile     string
	Threshold   float64
	TitleFilter string // active job-title filter, "" = all
	Summary     Summary
	Funnel      Funnel
	Weekly      []WeekRow
	Salaries    Salaries
	Companies   []Company
	Verdicts    []Verdict    // verdict breakdown of the (title-filtered) matches
	TopTitles   []Company    // top matched job titles — always unfiltered, feeds the chips
	Gaps        []TitleToken // frequent words in the evaluator's key_gap notes
}

func (r *Repo) Overview(ctx context.Context, profile, titleFilter string) (*Overview, error) {
	o := &Overview{Profile: profile, Threshold: thresholdDefault, TitleFilter: titleFilter}
	if err := r.summary(ctx, o); err != nil {
		return nil, err
	}
	if err := r.funnel(ctx, o); err != nil {
		return nil, err
	}
	if err := r.weekly(ctx, o); err != nil {
		return nil, err
	}
	if err := r.salaries(ctx, o); err != nil {
		return nil, err
	}
	if err := r.companies(ctx, o); err != nil {
		return nil, err
	}
	if err := r.verdicts(ctx, o); err != nil {
		return nil, err
	}
	if err := r.titles(ctx, o); err != nil {
		return nil, err
	}
	if err := r.gaps(ctx, o); err != nil {
		return nil, err
	}
	return o, nil
}

func (r *Repo) summary(ctx context.Context, o *Overview) error {
	err := r.q.QueryRow(ctx, `
        SELECT
          (SELECT count(*) FROM jobspy_jobs WHERE sys_profile = $1),
          count(DISTINCT e.job_id),
          (SELECT count(DISTINCT e2.job_id)
             FROM evaluated_jobs e2
             JOIN jobspy_jobs j ON j.id = e2.job_id AND j.sys_profile = e2.sys_profile
             WHERE e2.sys_profile = $1 AND e2.avg_score >= $2`+titleClause+`),
          COALESCE(avg(e.avg_score), 0)
        FROM evaluated_jobs e
        WHERE e.sys_profile = $1`,
		o.Profile, o.Threshold, o.TitleFilter,
	).Scan(&o.Summary.Scraped, &o.Summary.Evaluated, &o.Summary.Matches, &o.Summary.AvgScore)
	if err != nil {
		return err
	}
	return r.q.QueryRow(ctx, `
        SELECT
          count(*),
          count(*) FILTER (WHERE status IN ('applied','screen','interview')),
          count(*) FILTER (WHERE status IN ('screen','interview')),
          count(*) FILTER (WHERE final_status = 'offer'),
          count(*) FILTER (WHERE final_status = 'rejected')
        FROM web.job_review
        WHERE sys_profile = $1`,
		o.Profile,
	).Scan(&o.Summary.Reviewed, &o.Summary.Applied, &o.Summary.Interview,
		&o.Summary.Offers, &o.Summary.Rejected)
}

// funnel is unaffected by the verdict filter — it reports what the user did
// with their applications, not what the evaluator thought of the postings.
func (r *Repo) funnel(ctx context.Context, o *Overview) error {
	f := &o.Funnel
	return r.q.QueryRow(ctx, `
        SELECT
          count(*),
          count(*) FILTER (WHERE status IN ('screen','interview')),
          count(*) FILTER (WHERE status = 'interview'),
          count(*) FILTER (WHERE final_status = 'offer'),
          count(*) FILTER (WHERE final_status = 'rejected'),
          count(*) FILTER (WHERE final_status IS NULL AND status IN ('screen','interview')),
          count(*) FILTER (WHERE final_status IS NULL AND status = 'applied'
                             AND created_at >= now() - make_interval(days => $2)),
          count(*) FILTER (WHERE final_status IS NULL AND status = 'applied'
                             AND created_at <  now() - make_interval(days => $2))
        FROM web.job_review
        WHERE sys_profile = $1 AND status IN ('applied','screen','interview')`,
		o.Profile, GhostAfterDays,
	).Scan(&f.Applied, &f.Screen, &f.Interview, &f.Offers, &f.Rejected,
		&f.InProcess, &f.Waiting, &f.Ghosted)
}

func (r *Repo) weekly(ctx context.Context, o *Overview) error {
	rows, err := r.q.Query(ctx, `
        SELECT
          date_trunc('week', e.created_at)::date::text,
          count(DISTINCT e.job_id),
          count(DISTINCT e.job_id) FILTER (WHERE e.avg_score >= $2`+titleClause+`)
        FROM evaluated_jobs e
        LEFT JOIN jobspy_jobs j ON j.id = e.job_id AND j.sys_profile = e.sys_profile
        WHERE e.sys_profile = $1
          AND e.created_at >= now() - interval '13 weeks'
        GROUP BY 1 ORDER BY 1`,
		o.Profile, o.Threshold, o.TitleFilter)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var w WeekRow
		if err := rows.Scan(&w.Week, &w.Evals, &w.Matches); err != nil {
			return err
		}
		o.Weekly = append(o.Weekly, w)
	}
	return rows.Err()
}

// salaries pulls yearly-normalized comp midpoints for matched jobs (one row
// per job at its best score) and derives percentiles. Sub-20k and 600k+
// midpoints are dropped as scrape noise (hourly rates stored as yearly, etc).
func (r *Repo) salaries(ctx context.Context, o *Overview) error {
	rows, err := r.q.Query(ctx, `
        WITH best AS (
          SELECT DISTINCT ON (e.job_id) e.job_id
          FROM evaluated_jobs e
          JOIN jobspy_jobs j ON j.id = e.job_id AND j.sys_profile = e.sys_profile
          WHERE e.sys_profile = $1 AND e.avg_score >= $2`+titleClause+`
          ORDER BY e.job_id, e.avg_score DESC
        )
        SELECT
          ((j.min_amount::numeric + j.max_amount::numeric) / 2
            * CASE j.interval
                WHEN 'hourly'  THEN 2080
                WHEN 'daily'   THEN 260
                WHEN 'weekly'  THEN 52
                WHEN 'monthly' THEN 12
                ELSE 1
              END)::bigint,
          lower(j.title) ~ '\m(senior|sr|staff|principal|lead)\M'
        FROM best b
        JOIN jobspy_jobs j ON j.id = b.job_id AND j.sys_profile = $1
        WHERE j.min_amount ~ '^[0-9]+(\.[0-9]+)?$'
          AND j.max_amount ~ '^[0-9]+(\.[0-9]+)?$'
          AND j.min_amount::numeric > 0`,
		o.Profile, o.Threshold, o.TitleFilter)
	if err != nil {
		return err
	}
	defer rows.Close()
	var mids []SalaryMid
	for rows.Next() {
		var m int64
		var sr bool
		if err := rows.Scan(&m, &sr); err != nil {
			return err
		}
		if m >= 20_000 && m <= 600_000 {
			mids = append(mids, SalaryMid{Mid: int(m), Senior: sr})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	sort.Slice(mids, func(a, b int) bool { return mids[a].Mid < mids[b].Mid })
	ints := make([]int, len(mids))
	for i, m := range mids {
		ints[i] = m.Mid
	}
	s := &o.Salaries
	s.Mids = mids
	s.N = len(mids)
	if s.N == 0 {
		return nil
	}
	s.Min, s.Max = ints[0], ints[s.N-1]
	s.P25 = percentile(ints, 0.25)
	s.Median = percentile(ints, 0.50)
	s.P75 = percentile(ints, 0.75)
	return nil
}

// percentile is the nearest-rank percentile of a sorted slice.
func percentile(sorted []int, p float64) int {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func (r *Repo) companies(ctx context.Context, o *Overview) error {
	rows, err := r.q.Query(ctx, `
        SELECT j.company, count(DISTINCT e.job_id) AS n,
               round(avg(e.avg_score)::numeric, 1)
        FROM evaluated_jobs e
        JOIN jobspy_jobs j ON j.id = e.job_id AND j.sys_profile = e.sys_profile
        WHERE e.sys_profile = $1 AND e.avg_score >= $2`+titleClause+`
          AND COALESCE(j.company, '') <> ''
        GROUP BY 1
        HAVING count(DISTINCT e.job_id) >= 2
        ORDER BY n DESC, 3 DESC
        LIMIT 12`,
		o.Profile, o.Threshold, o.TitleFilter)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c Company
		if err := rows.Scan(&c.Name, &c.N, &c.AvgScore); err != nil {
			return err
		}
		o.Companies = append(o.Companies, c)
	}
	return rows.Err()
}

func (r *Repo) verdicts(ctx context.Context, o *Overview) error {
	rows, err := r.q.Query(ctx, `
        SELECT COALESCE(NULLIF(e.reasoning::jsonb->>'verdict', ''), 'unknown'),
               count(DISTINCT e.job_id)
        FROM evaluated_jobs e
        JOIN jobspy_jobs j ON j.id = e.job_id AND j.sys_profile = e.sys_profile
        WHERE e.sys_profile = $1 AND e.avg_score >= $2`+titleClause+`
          AND e.reasoning IS NOT NULL AND left(e.reasoning, 1) = '{'
        GROUP BY 1 ORDER BY 2 DESC`,
		o.Profile, o.Threshold, o.TitleFilter)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v Verdict
		if err := rows.Scan(&v.Name, &v.N); err != nil {
			return err
		}
		o.Verdicts = append(o.Verdicts, v)
	}
	return rows.Err()
}

// titles ranks title FAMILIES among matches (normTitleExpr buckets — "Lead /
// Senior / II" variants fold into one row), same shape as the companies list:
// "which roles match me most, and how well".
func (r *Repo) titles(ctx context.Context, o *Overview) error {
	rows, err := r.q.Query(ctx, `
        SELECT k, initcap(k), n, sr, avg
        FROM (
          SELECT `+normTitleExpr+` AS k,
                 count(DISTINCT e.job_id) AS n,
                 count(DISTINCT e.job_id) FILTER (
                   WHERE lower(j.title) ~ '\m(senior|sr|staff|principal|lead)\M') AS sr,
                 round(avg(e.avg_score)::numeric, 1) AS avg
          FROM evaluated_jobs e
          JOIN jobspy_jobs j ON j.id = e.job_id AND j.sys_profile = e.sys_profile
          WHERE e.sys_profile = $1 AND e.avg_score >= $2
            AND COALESCE(j.title, '') <> ''
          GROUP BY 1
        ) d
        WHERE k <> ''
        ORDER BY n DESC, avg DESC
        LIMIT 12`,
		o.Profile, o.Threshold)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var t Company
		if err := rows.Scan(&t.Key, &t.Name, &t.N, &t.Senior, &t.AvgScore); err != nil {
			return err
		}
		o.TopTitles = append(o.TopTitles, t)
	}
	return rows.Err()
}

// gaps tokenizes the evaluator's free-text key_gap note across matched jobs
// and counts, per token, how many distinct jobs flagged it. The stopword list
// strips the sentence scaffolding ("experience ... required but not covered")
// so what's left is mostly the missing tech/domain itself. The candidate's own
// stack can still appear (gap notes often contrast against it) — treat the
// list as "themes", not a strict miss ranking.
func (r *Repo) gaps(ctx context.Context, o *Overview) error {
	rows, err := r.q.Query(ctx, `
        WITH g AS (
          SELECT DISTINCT ON (e.job_id) e.job_id,
                 e.reasoning::jsonb->>'key_gap' AS gap
          FROM evaluated_jobs e
          JOIN jobspy_jobs j ON j.id = e.job_id AND j.sys_profile = e.sys_profile
          WHERE e.sys_profile = $1 AND e.avg_score >= $2
            AND left(e.reasoning, 1) = '{'`+titleClause+`
          ORDER BY e.job_id, e.avg_score DESC
        ), tokens AS (
          SELECT job_id, regexp_split_to_table(lower(gap), '[^a-z0-9+#]+') AS tok
          FROM g WHERE gap IS NOT NULL
        ),
        -- The profile's own field words ("data", "engineer", …) show up in gap
        -- notes as context, not as misses. Exclude tokens that appear in >20%
        -- of matched titles — the self-descriptors — while keeping rarer title
        -- tech (azure, kafka) that can be a genuine miss elsewhere.
        matched AS (
          SELECT DISTINCT ON (e.job_id) e.job_id
          FROM evaluated_jobs e
          WHERE e.sys_profile = $1 AND e.avg_score >= $2
          ORDER BY e.job_id, e.avg_score DESC
        ),
        self AS (
          SELECT regexp_split_to_table(lower(j.title), '[^a-z0-9+#]+') AS tok
          FROM matched b
          JOIN jobspy_jobs j ON j.id = b.job_id AND j.sys_profile = $1
        ),
        selftop AS (
          SELECT tok FROM self WHERE length(tok) >= 3
          GROUP BY tok
          HAVING count(*) > 0.2 * (SELECT count(*) FROM matched)
        )
        SELECT tok, count(DISTINCT job_id) AS n
        FROM tokens
        WHERE tok NOT IN (SELECT tok FROM selftop)
          AND length(tok) >= 3
          AND tok NOT IN (
            'the','and','but','not','with','for','are','has','have','had',
            'this','that','from','over','into','while','though','than','then',
            'his','her','their','they','its','was','were','will','would','may',
            'can','could','also','only','some','such','both','more','most',
            'which','what','when','where','all','any','out','vs','via','per',
            'experience','experiences','experienced','required','require',
            'requires','requirement','requirements','specific','specifically',
            'explicit','explicitly','covered','cover','covers','candidate',
            'candidates','background','mention','mentioned','mentions','lack',
            'lacks','lacking','missing','miss','gap','gaps','role','roles',
            'job','jobs','resume','work','working','knowledge','skill','skills',
            'expertise','exposure','hands','direct','directly','formal','deep',
            'strong','senior','years','year','needed','need','needs','must',
            'prefer','preferred','similar','related','unclear','evident',
            'demonstrated','demonstrate','shown','show','shows','listed','list',
            'stated','state','primary','key','main','core','focus','focused',
            'level','familiarity','familiar','proficiency','proficient',
            'professional','profile','include','includes','including','use',
            'used','using','tool','tools','technology','technologies','tech',
            'stack','specialized','no','none','however','rather','instead',
            'does','doesnt','isnt','current','currently','limited','title',
            'titles','position','positions','seniority','oriented','based',
            'versus','across','environment','environments','company','large',
            'scale','specialization','emphasis','context','contexts','despite',
            'compared','comparison','below','above','beyond','without',
            'against','toward','towards','between','either','neither',
            'engineering','development','develop','developing','lead','leads',
            'leading')
        GROUP BY 1 ORDER BY n DESC
        LIMIT 14`,
		o.Profile, o.Threshold, o.TitleFilter)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var t TitleToken
		if err := rows.Scan(&t.Token, &t.N); err != nil {
			return err
		}
		o.Gaps = append(o.Gaps, t)
	}
	return rows.Err()
}
