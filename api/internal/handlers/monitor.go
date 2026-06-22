package handlers

import (
	"html/template"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/greenmushrooms/job_searcher_web/api/internal/chart"
	"github.com/greenmushrooms/job_searcher_web/api/internal/metrics"
	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
)

// MonitorHandler serves the per-profile search-metrics dashboard (/monitor):
// a server-rendered, zero-JS page of inline-SVG charts.
type MonitorHandler struct {
	Pool     *pgxpool.Pool
	Renderer *render.Renderer
}

type dayOpt struct {
	Days     int
	Label    string
	Selected bool
}

type monitorView struct {
	Profile     string
	Profiles    []string
	Days        int
	DayOpts     []dayOpt
	FunnelSVG   template.HTML
	ScoresSVG   template.HTML
	SalarySVG   template.HTML
	PostingsSVG template.HTML
	SalaryWith  int
	SalaryTotal int
	SalaryPct   int
	HasPostings bool
}

// Monitor renders the dashboard for ?profile and ?days (0=all, else 7/30/90).
func (h *MonitorHandler) Monitor(w http.ResponseWriter, r *http.Request) {
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))
	days := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && (v == 7 || v == 30 || v == 90) {
		days = v
	}

	rep, err := metrics.Load(r.Context(), h.Pool, profile, days)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "metrics: "+err.Error())
		return
	}

	view := monitorView{
		Profile:     profile,
		Profiles:    profiles.Known(r.Context()),
		Days:        days,
		FunnelSVG:   chart.Funnel(rep.Funnel, 520, 150),
		ScoresSVG:   chart.BarChart(rep.Scores, 520, 190),
		SalarySVG:   chart.BarChart(rep.Salaries, 520, 190),
		PostingsSVG: chart.BarChart(rep.Postings, 520, 190),
		SalaryWith:  rep.SalaryWith,
		SalaryTotal: rep.SalaryTotal,
		HasPostings: len(rep.Postings) > 0,
	}
	if rep.SalaryTotal > 0 {
		view.SalaryPct = int(100 * float64(rep.SalaryWith) / float64(rep.SalaryTotal))
	}
	for _, o := range []dayOpt{{0, "All time", false}, {7, "Last 7 days", false}, {30, "Last 30 days", false}, {90, "Last 90 days", false}} {
		o.Selected = o.Days == days
		view.DayOpts = append(view.DayOpts, o)
	}

	h.Renderer.HTML(w, http.StatusOK, "monitor", view)
}
