package handlers

import (
	"net/http"

	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
	"github.com/greenmushrooms/job_searcher_web/api/internal/stats"
)

// WelcomeHandler serves the demo instance's landing page ("/" when
// DEMO_PROFILE is set): what this is, how the real pipeline runs, and a
// feature tour — with live pipeline totals for the hero numbers.
type WelcomeHandler struct {
	Stats    *stats.Repo
	Renderer *render.Renderer
}

type welcomeView struct {
	Scraped, Evaluated, Matches string
}

func (h *WelcomeHandler) Page(w http.ResponseWriter, r *http.Request) {
	profile := profiles.Resolve(r.Context(), "")
	v := welcomeView{Scraped: "—", Evaluated: "—", Matches: "—"}
	if o, err := h.Stats.Overview(r.Context(), profile, ""); err == nil {
		v.Scraped = groupInt(o.Summary.Scraped)
		v.Evaluated = groupInt(o.Summary.Evaluated)
		v.Matches = groupInt(o.Summary.Matches)
	}
	h.Renderer.HTML(w, http.StatusOK, "welcome", v)
}
