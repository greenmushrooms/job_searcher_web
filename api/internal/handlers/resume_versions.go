package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
)

// versionView is one entry in the diff-lab right-pane version dropdown. Markdown
// is inlined so switching versions needs no extra round-trip (a job has only a
// handful of saved versions, each a few KB).
type versionView struct {
	Version     int    `json:"version"`
	GeneratedAt string `json:"generatedAt"`
	IsCurrent   bool   `json:"isCurrent"`
	Markdown    string `json:"markdown"`
}

// ResumeVersions handles GET /ui/jobs/{id}/resume/versions?profile=… — the full
// SCD Type 2 history of saved tailored résumés for a job, newest first.
func (h *ResumeHandler) ResumeVersions(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))

	hist, err := h.Finalizations.History(r.Context(), jobID, profile)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]versionView, 0, len(hist))
	for _, f := range hist {
		out = append(out, versionView{
			Version:     f.Version,
			GeneratedAt: f.GeneratedAt,
			IsCurrent:   f.IsCurrent,
			Markdown:    f.Markdown,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// RestoreResumeVersion handles POST /ui/jobs/{id}/resume/versions/{version}/restore
// — copies a past version's content into a new current version (SCD Type 2, so
// the existing current is expired, not overwritten). Returns the new version
// number as JSON.
func (h *ResumeHandler) RestoreResumeVersion(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad version")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "bad form")
		return
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))

	f, err := h.Finalizations.Restore(r.Context(), jobID, profile, version)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"version": f.Version})
}
