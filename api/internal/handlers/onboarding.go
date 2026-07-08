package handlers

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
)

// OnboardingHandler serves the first-run hints fragment on the jobs page:
// present while the profile still lacks a résumé or saved searches, empty
// (204-ish) once both exist — so it retires itself as the user completes
// setup. Permanent dismissal is client-side (localStorage checkbox).
type OnboardingHandler struct {
	Pool     *pgxpool.Pool
	Renderer *render.Renderer
}

type onboardingView struct {
	HasResume   bool
	HasSearches bool
}

func (h *OnboardingHandler) Fragment(w http.ResponseWriter, r *http.Request) {
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))
	var v onboardingView
	err := h.Pool.QueryRow(r.Context(), `
        SELECT EXISTS (SELECT 1 FROM web.resume_master WHERE sys_profile = $1),
               EXISTS (SELECT 1 FROM web.job_searches  WHERE sys_profile = $1)`,
		profile,
	).Scan(&v.HasResume, &v.HasSearches)
	if err != nil || (v.HasResume && v.HasSearches) {
		w.WriteHeader(http.StatusOK) // empty body — nothing to show
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "onboarding", v)
}
