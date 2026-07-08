package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
	"github.com/greenmushrooms/job_searcher_web/api/internal/stats"
)

// StatsHandler serves the per-profile stats page (zero-JS, server-rendered
// CSS charts).
type StatsHandler struct {
	Stats    *stats.Repo
	Renderer *render.Renderer
}

// ── view models ──────────────────────────────────────────────────────────────
// All chart geometry (bar percentages, bins, label selection) is computed here
// so the templates stay declarative.

type statsTile struct {
	Label string
	Value string
	Note  string // optional small line under the value
}

type statsBarPair struct {
	Week      string // "Apr 14"
	Evals     int
	Matches   int
	EvalPct   int  // bar height/width, % of the max week
	MatchPct  int  // % of the same max, so the pair shares one scale
	Rate      int  // matches/evals %
	Highlight bool // latest week — gets the direct label
}

type statsSalaryBin struct {
	Label   string // "80–100k"
	N       int
	Pct     int  // bar height, % of modal bin
	IsModal bool // tallest bin — gets the direct label
}

type statsCompanyRow struct {
	Name     string
	N        int
	Pct      int    // bar width, % of max
	AvgScore string
	Href     string // set on title rows — clicking filters the page by that title
	// Title rows only: seniority split of N on the same scale as Pct.
	SrN     int
	SrPct   int // senior-tier segment width
	RestPct int // remaining segment width
}

type statsVerdictRow struct {
	Name string
	N    int
	Pct  int // share of all verdicts, for the meter
}

type statsTokenRow struct {
	Token string
	N     int
	Pct   int
}

// statsFunnelRow is one cumulative stage of the application funnel; Class picks
// the ordinal-ramp step in the template CSS.
type statsFunnelRow struct {
	Label string
	N     int
	Pct   int    // bar width, % of applications submitted
	Share string // same ratio as text, e.g. "34%"
	Class string // fn-1..fn-4
}

// statsOutcomeRow is one outcome bucket; the five buckets partition Applied.
// Class picks a status color (labels + counts carry the meaning, not color).
type statsOutcomeRow struct {
	Label string
	N     int
	Pct   int
	Share string
	Class string // oc-run / oc-offer / oc-reject / oc-wait / oc-ghost
}

// statsChip is one entry in the verdict filter row.
type statsChip struct {
	Label  string
	N      int
	Href   string
	Active bool
}

type statsView struct {
	Profile   string
	Threshold string

	Tiles []statsTile

	Weekly    []statsBarPair
	WeeklyMax int // y-axis top (max evals in a week)
	HasWeekly bool

	SalaryBins []statsSalaryBin
	Salary     stats.Salaries
	SalaryP25  string
	SalaryP50  string
	SalaryP75  string
	SalaryMin  string
	SalaryMax  string
	// Percentile marker positions across the histogram x-range, in %.
	P25Pos, P50Pos, P75Pos int
	IQRWidth               int // P75Pos - P25Pos, for the band fill
	HasSalary              bool

	Companies []statsCompanyRow
	Verdicts  []statsVerdictRow
	TitleRows []statsCompanyRow // top matched job titles, companies-style
	Gaps      []statsTokenRow

	// Job-title filter state: "" = all matches.
	FilterTitle string
	Chips       []statsChip

	// Application funnel: cumulative stages + outcome split of Applied.
	Funnel    []statsFunnelRow
	Outcomes  []statsOutcomeRow
	HasFunnel bool
	GhostDays int

	Summary stats.Summary
}

// Page handles GET /stats — the full stats page. ?title=data+engineer narrows
// every match-derived section (salary, weekly matches, companies, verdicts,
// gaps) to that title family.
func (h *StatsHandler) Page(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	profile := profiles.Resolve(r.Context(), q.Get("profile"))
	o, err := h.Stats.Overview(r.Context(), profile, q.Get("title"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "stats", buildStatsView(o, q.Get("profile")))
}

func buildStatsView(o *stats.Overview, profileParam string) statsView {
	v := statsView{
		Profile:     o.Profile,
		Threshold:   trimFloat(o.Threshold),
		Summary:     o.Summary,
		FilterTitle: o.TitleFilter,
	}

	// Filter chips: All + the top matched job titles. Hrefs keep the explicit
	// ?profile= param when one was given (harmless when pinned).
	chipHref := func(title string) string {
		vals := url.Values{}
		if profileParam != "" {
			vals.Set("profile", profileParam)
		}
		if title != "" {
			vals.Set("title", title)
		}
		if enc := vals.Encode(); enc != "" {
			return "?" + enc
		}
		return "/stats"
	}
	activeTitle := func(key string) bool { return strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(o.TitleFilter)) }
	allN := 0
	for _, t := range o.TopTitles {
		allN += t.N
		if activeTitle(t.Key) {
			v.FilterTitle = t.Name // show the display form, not the lowercase key
		}
	}
	v.Chips = []statsChip{{Label: "All titles", N: allN, Href: chipHref(""), Active: o.TitleFilter == ""}}
	for i, t := range o.TopTitles {
		if i >= 8 { // chips row stays one line-ish; the full list is the titles section
			break
		}
		v.Chips = append(v.Chips, statsChip{
			Label: t.Name, N: t.N, Href: chipHref(t.Key), Active: activeTitle(t.Key),
		})
	}

	matchRate := 0.0
	if o.Summary.Evaluated > 0 {
		matchRate = 100 * float64(o.Summary.Matches) / float64(o.Summary.Evaluated)
	}
	matchLabel := "Matches ≥ " + v.Threshold
	if v.FilterTitle != "" {
		matchLabel += " · " + v.FilterTitle
	}
	v.Tiles = []statsTile{
		{Label: "Jobs scraped", Value: groupInt(o.Summary.Scraped)},
		{Label: "Jobs evaluated", Value: groupInt(o.Summary.Evaluated),
			Note: "avg score " + fmt.Sprintf("%.1f", o.Summary.AvgScore)},
		{Label: matchLabel, Value: groupInt(o.Summary.Matches),
			Note: fmt.Sprintf("%.1f%% of evaluated", matchRate)},
		{Label: "Applied", Value: groupInt(o.Summary.Applied),
			Note: fmt.Sprintf("%d reached interview", o.Summary.Interview)},
	}
	if o.Salaries.N > 0 {
		v.Tiles = append(v.Tiles, statsTile{
			Label: "Median salary (matches)",
			Value: money(o.Salaries.Median),
			Note:  fmt.Sprintf("across %d postings with pay", o.Salaries.N),
		})
	}

	// Weekly pairs — one shared scale (max evals) so the two series compare.
	maxEvals := 0
	for _, wk := range o.Weekly {
		if wk.Evals > maxEvals {
			maxEvals = wk.Evals
		}
	}
	v.WeeklyMax = maxEvals
	v.HasWeekly = len(o.Weekly) > 0 && maxEvals > 0
	for i, wk := range o.Weekly {
		p := statsBarPair{
			Week:      shortDate(wk.Week),
			Evals:     wk.Evals,
			Matches:   wk.Matches,
			Highlight: i == len(o.Weekly)-1,
		}
		if maxEvals > 0 {
			p.EvalPct = pctOf(wk.Evals, maxEvals)
			p.MatchPct = pctOf(wk.Matches, maxEvals)
		}
		if wk.Evals > 0 {
			p.Rate = int(100*float64(wk.Matches)/float64(wk.Evals) + 0.5)
		}
		v.Weekly = append(v.Weekly, p)
	}

	buildFunnel(&v, o.Funnel)

	buildSalaryBins(&v, o.Salaries)

	maxN := 0
	for _, c := range o.Companies {
		if c.N > maxN {
			maxN = c.N
		}
	}
	for _, c := range o.Companies {
		v.Companies = append(v.Companies, statsCompanyRow{
			Name: c.Name, N: c.N, Pct: pctOf(c.N, maxN),
			AvgScore: fmt.Sprintf("%.1f", c.AvgScore),
		})
	}

	totalV := 0
	for _, vd := range o.Verdicts {
		totalV += vd.N
	}
	for _, vd := range o.Verdicts {
		v.Verdicts = append(v.Verdicts, statsVerdictRow{
			Name: vd.Name, N: vd.N, Pct: pctOf(vd.N, totalV),
		})
	}

	maxT := 0
	for _, t := range o.TopTitles {
		if t.N > maxT {
			maxT = t.N
		}
	}
	for _, t := range o.TopTitles {
		v.TitleRows = append(v.TitleRows, statsCompanyRow{
			Name: t.Name, N: t.N, Pct: pctOf(t.N, maxT),
			AvgScore: fmt.Sprintf("%.1f", t.AvgScore),
			Href:     chipHref(t.Key),
			SrN:      t.Senior,
			SrPct:    pctOf(t.Senior, maxT),
			RestPct:  pctOf(t.N-t.Senior, maxT),
		})
	}

	maxG := 0
	for _, g := range o.Gaps {
		if g.N > maxG {
			maxG = g.N
		}
	}
	for _, g := range o.Gaps {
		v.Gaps = append(v.Gaps, statsTokenRow{Token: g.Token, N: g.N, Pct: pctOf(g.N, maxG)})
	}

	return v
}

// buildFunnel shapes the application funnel: cumulative stage rows on a shared
// scale (% of applications submitted) and the outcome split of that same total.
func buildFunnel(v *statsView, f stats.Funnel) {
	v.HasFunnel = f.Applied > 0
	v.GhostDays = stats.GhostAfterDays
	if !v.HasFunnel {
		return
	}
	share := func(n int) string { return strconv.Itoa(pctOf(n, f.Applied)) + "%" }
	stage := func(label string, n int, class string) statsFunnelRow {
		return statsFunnelRow{Label: label, N: n, Pct: pctOf(n, f.Applied), Share: share(n), Class: class}
	}
	v.Funnel = []statsFunnelRow{
		stage("Applied", f.Applied, "fn-1"),
		stage("Screen", f.Screen, "fn-2"),
		stage("Interview", f.Interview, "fn-3"),
		stage("Offer", f.Offers, "fn-4"),
	}
	outcome := func(label string, n int, class string) statsOutcomeRow {
		return statsOutcomeRow{Label: label, N: n, Pct: pctOf(n, f.Applied), Share: share(n), Class: class}
	}
	v.Outcomes = []statsOutcomeRow{
		outcome("In process", f.InProcess, "oc-run"),
		outcome("Awaiting reply", f.Waiting, "oc-wait"),
		outcome("Ghosted", f.Ghosted, "oc-ghost"),
		outcome("Rejected", f.Rejected, "oc-reject"),
		outcome("Offer", f.Offers, "oc-offer"),
	}
}

// buildSalaryBins buckets the sorted midpoints into 20k bins (40k when the
// range would need more than ~14 bins) and marks the modal bin.
func buildSalaryBins(v *statsView, s stats.Salaries) {
	v.HasSalary = s.N > 0
	if s.N == 0 {
		return
	}
	v.Salary = s
	v.SalaryP25, v.SalaryP50, v.SalaryP75 = money(s.P25), money(s.Median), money(s.P75)
	v.SalaryMin, v.SalaryMax = money(s.Min), money(s.Max)

	width := 20_000
	if (s.Max-s.Min)/width > 14 {
		width = 40_000
	}
	lo := s.Min / width * width
	hi := (s.Max/width + 1) * width

	counts := map[int]int{}
	for _, m := range s.Mids {
		counts[(m-lo)/width]++
	}
	nBins := (hi - lo) / width
	modal, modalIdx := 0, 0
	for i := 0; i < nBins; i++ {
		if counts[i] > modal {
			modal, modalIdx = counts[i], i
		}
	}
	for i := 0; i < nBins; i++ {
		b := statsSalaryBin{
			Label:   binLabel(lo+i*width, lo+(i+1)*width),
			N:       counts[i],
			IsModal: i == modalIdx,
		}
		if modal > 0 {
			b.Pct = pctOf(counts[i], modal)
		}
		v.SalaryBins = append(v.SalaryBins, b)
	}
	span := hi - lo
	if span > 0 {
		v.P25Pos = pctOf(s.P25-lo, span)
		v.P50Pos = pctOf(s.Median-lo, span)
		v.P75Pos = pctOf(s.P75-lo, span)
		if v.IQRWidth = v.P75Pos - v.P25Pos; v.IQRWidth < 1 {
			v.IQRWidth = 1
		}
	}
}

// ── tiny formatters ──────────────────────────────────────────────────────────

func pctOf(n, max int) int {
	if max <= 0 {
		return 0
	}
	return int(100*float64(n)/float64(max) + 0.5)
}

// money renders a yearly amount compactly: $87k / $142k / $1.2M.
func money(n int) string {
	switch {
	case n >= 1_000_000:
		return "$" + strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + "M"
	case n >= 1_000:
		return "$" + strconv.Itoa((n+500)/1_000) + "k"
	default:
		return "$" + strconv.Itoa(n)
	}
}

func binLabel(lo, hi int) string {
	return strconv.Itoa(lo/1000) + "–" + strconv.Itoa(hi/1000) + "k"
}

// groupInt renders 12345 as "12,345".
func groupInt(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// shortDate turns "2026-04-14" into "Apr 14".
func shortDate(iso string) string {
	if len(iso) < 10 {
		return iso
	}
	months := map[string]string{
		"01": "Jan", "02": "Feb", "03": "Mar", "04": "Apr", "05": "May",
		"06": "Jun", "07": "Jul", "08": "Aug", "09": "Sep", "10": "Oct",
		"11": "Nov", "12": "Dec",
	}
	m, d := iso[5:7], iso[8:10]
	if d[0] == '0' {
		d = d[1:]
	}
	if name, ok := months[m]; ok {
		return name + " " + d
	}
	return iso
}
