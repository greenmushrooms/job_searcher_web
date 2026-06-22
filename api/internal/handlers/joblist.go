package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/applications"
	"github.com/greenmushrooms/job_searcher_web/api/internal/jobs"
	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
	"github.com/greenmushrooms/job_searcher_web/api/internal/templates"
)

// JobUIHandler serves the server-rendered (htmx) job list + summary. The list
// shows each job's review state as a ✓/✗/★ badge; a job with no decision yet
// renders "unread". Apply/Skip/Interview live in the summary and OOB-swap the
// matching list row so the badge updates without a full list reload.
type JobUIHandler struct {
	Jobs      *jobs.Repo
	Apps      *applications.Repo
	Templates *templates.Repo
	Renderer  *render.Renderer
}

type jobRowView struct {
	ID       string
	Profile  string
	Title    string
	Company  string
	Location string
	IsRemote bool
	Score     string
	EvalDate  string
	Status    string // "" == unread (no decision yet)
	Final     string // "" == still open; else terminal outcome (rejected/offer)
	HasResume bool   // a saved résumé exists for this job → 📄 badge
	OOB       bool   // render with hx-swap-oob for out-of-band row updates
}

type jobListView struct {
	Profile string
	Rows    []jobRowView
}

type jobSummaryView struct {
	ID           string
	Profile      string
	Title        string
	URL          string
	Company      string
	Location     string
	IsRemote     bool
	DatePosted   string
	EvalDate     string
	Score        string
	Verdict      string
	Summary      string
	TechStack    string
	Compensation string
	Status       string // "" == undecided
	Final        string // "" == still open; else terminal outcome (rejected/offer)
}

// jobStatusUpdateView renders the summary (main swap target) plus the updated
// row (OOB) in one response.
type jobStatusUpdateView struct {
	Summary jobSummaryView
	Row     jobRowView
}

// defaultListDays is how far back the job list reaches when the filter bar
// hasn't asked for anything else: the last two weeks.
const defaultListDays = 14

// JobList handles GET /ui/jobs — the left-hand list fragment.
func (h *JobUIHandler) JobList(w http.ResponseWriter, r *http.Request) {
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))
	q := r.URL.Query()
	p := jobs.ListParams{Profile: profile, MinScore: 6.9, Limit: 50, DateField: "eval"}
	if s := q.Get("min_score"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			p.MinScore = v
		}
	}
	if s := q.Get("limit"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			p.Limit = clampInt(v, 1, 200)
		}
	}
	if v := q.Get("date_field"); v == "posted" || v == "eval" {
		p.DateField = v
	}
	p.From, p.To = q.Get("from"), q.Get("to")
	// "days" is the filter bar's date-range dropdown: jobs from the last N
	// days (0 = all time). It only applies when no explicit from= is given;
	// with neither present the list defaults to the last two weeks.
	if p.From == "" {
		days := defaultListDays
		if s := q.Get("days"); s != "" {
			if v, err := strconv.Atoi(s); err == nil && v >= 0 {
				days = v
			}
		}
		if days > 0 {
			p.From = time.Now().AddDate(0, 0, -days).Format("2006-01-02")
		}
	}
	if v := q.Get("status"); v == "inbox" || v == "applied" || v == "screen" ||
		v == "interview" || v == "skipped" || v == "rejected" || v == "offer" {
		p.Status = v
	}
	// Free-text search behaves like an email search box: a query matches across
	// title/company/location regardless of score or date, so drop the score
	// floor and the default date window when one is present.
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		p.Q = s
		p.MinScore = 0
		p.From, p.To = "", ""
	}

	list, err := h.Jobs.ListLite(r.Context(), p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	view := jobListView{Profile: profile}
	for _, j := range list {
		view.Rows = append(view.Rows, toRowView(j, profile))
	}
	h.Renderer.HTML(w, http.StatusOK, "job_list", view)
}

// jobWorkspaceView wraps the summary plus the ids the workspace shell needs to
// lazy-load the draft for this job.
type jobWorkspaceView struct {
	JobID    string
	Profile  string
	Summary  jobSummaryView
	Controls resumeControlsView
}

// JobWorkspace handles GET /ui/jobs/{id}/workspace — the per-job right pane:
// the summary on top, then a container that lazy-loads the tailoring draft. The
// summary re-renders on apply/skip without disturbing the draft below it.
func (h *JobUIHandler) JobWorkspace(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))
	j, err := h.Jobs.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if j == nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	controls, err := resumeControls(r.Context(), h.Templates, id, profile, "", false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "job_workspace", jobWorkspaceView{
		JobID:    id,
		Profile:  profile,
		Summary:  toSummaryView(*j, profile),
		Controls: controls,
	})
}

// JobSummary handles GET /ui/jobs/{id}/summary — the right-hand detail.
func (h *JobUIHandler) JobSummary(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))
	j, err := h.Jobs.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if j == nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "job_summary", toSummaryView(*j, profile))
}

// RowStatus handles POST /ui/jobs/{id}/row-status — a review transition (a stage
// like applied/screen/interview/skipped, or an outcome like rejected/offer). It
// upserts job_review, then renders the refreshed summary plus an OOB row.
func (h *JobUIHandler) RowStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	status, _, profile := parseStatusInputs(r)
	profile = profiles.Resolve(r.Context(), profile)

	_, err := h.Apps.Upsert(r.Context(), id, profile, status, nil)
	switch {
	case errors.Is(err, applications.ErrInvalidStatus):
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	case errors.Is(err, applications.ErrJobNotFound):
		http.Error(w, "job not found", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	j, err := h.Jobs.Get(r.Context(), id)
	if err != nil || j == nil {
		http.Error(w, "reload job after status change", http.StatusInternalServerError)
		return
	}
	row := toRowView(*j, profile)
	row.OOB = true
	h.Renderer.HTML(w, http.StatusOK, "job_status_update", jobStatusUpdateView{
		Summary: toSummaryView(*j, profile),
		Row:     row,
	})
}

// ── view mappers ────────────────────────────────────────────────────────────

func toRowView(j jobs.Job, profile string) jobRowView {
	v := jobRowView{
		ID:        j.ID,
		Profile:   profile,
		Title:     derefOr(j.Title, "(no title)"),
		Company:   derefOr(j.Company, ""),
		Location:  derefOr(j.Location, ""),
		IsRemote:  j.IsRemote != nil && *j.IsRemote,
		Score:     fmtScore(j.Score),
		EvalDate:  dateOnly(j.EvalDate),
		HasResume: j.HasResume,
	}
	if j.Application != nil {
		v.Status = j.Application.Status
		if j.Application.FinalStatus != nil {
			v.Final = *j.Application.FinalStatus
		}
	}
	return v
}

func toSummaryView(j jobs.Job, profile string) jobSummaryView {
	v := jobSummaryView{
		ID:         j.ID,
		Profile:    profile,
		Title:      derefOr(j.Title, "(no title)"),
		URL:        derefOr(j.URL, ""),
		Company:    derefOr(j.Company, ""),
		Location:   derefOr(j.Location, ""),
		IsRemote:   j.IsRemote != nil && *j.IsRemote,
		DatePosted: derefOr(j.DatePosted, "?"),
		EvalDate:   dateOnly(j.EvalDate),
		Score:      fmtScore(j.Score),
		Verdict:    reasoningStr(j.Reasoning, "verdict"),
		Summary:    reasoningStr(j.Reasoning, "summary"),
		TechStack:  reasoningStr(j.Reasoning, "tech_stack"),
	}
	if j.Application != nil {
		v.Status = j.Application.Status
		if j.Application.FinalStatus != nil {
			v.Final = *j.Application.FinalStatus
		}
	}
	if c := j.Compensation; c != nil && c.Min != nil && c.Max != nil {
		v.Compensation = strconv.FormatInt(*c.Min, 10) + "–" + strconv.FormatInt(*c.Max, 10)
		if c.Currency != nil {
			v.Compensation += " " + *c.Currency
		}
		if c.Interval != nil {
			v.Compensation += " " + *c.Interval
		}
	}
	return v
}

func derefOr(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}

func fmtScore(f *float64) string {
	if f == nil {
		return "—"
	}
	return strconv.FormatFloat(*f, 'f', 1, 64)
}

// dateOnly trims a timestamp string to its YYYY-MM-DD prefix.
func dateOnly(s *string) string {
	if s == nil || len(*s) < 10 {
		if s == nil {
			return "?"
		}
		return *s
	}
	return (*s)[:10]
}

func reasoningStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
