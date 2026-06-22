package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/applications"
	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
)

type UIHandler struct {
	Repo     *applications.Repo
	Renderer *render.Renderer
}

type statusRowView struct {
	JobID      string
	SysProfile string
	Status     string
	Final      string // "" == still open; else terminal outcome (rejected/offer)
	Notes      string
	UpdatedAt  string
}

// StatusRow handles POST /ui/jobs/{id}/status-row — same Upsert as the JSON
// endpoint, but returns the rendered HTML row so htmx can swap it in-place
// via hx-swap=outerHTML.
//
// Accepts both JSON bodies (for parity with the API) and form-encoded bodies
// (the natural shape from an htmx form). status/notes/profile are read either
// way; profile defaults to "Slava".
func (h *UIHandler) StatusRow(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	status, notesStr, profile := parseStatusInputs(r)
	profile = profiles.Resolve(r.Context(), profile)
	var notes *string
	if notesStr != "" {
		notes = &notesStr
	}

	app, err := h.Repo.Upsert(r.Context(), jobID, profile, status, notes)
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

	view := statusRowView{
		JobID:      app.JobID,
		SysProfile: app.SysProfile,
		Status:     app.Status,
		UpdatedAt:  app.UpdatedAt,
	}
	if app.FinalStatus != nil {
		view.Final = *app.FinalStatus
	}
	if app.Notes != nil {
		view.Notes = *app.Notes
	}
	h.Renderer.HTML(w, http.StatusOK, "status_row", view)
}

// parseStatusInputs accepts either JSON or form-encoded bodies and returns
// (status, notes, profile). Empty string == not provided.
func parseStatusInputs(r *http.Request) (status, notes, profile string) {
	ct := r.Header.Get("Content-Type")
	if len(ct) >= 16 && ct[:16] == "application/json" {
		var body struct {
			Status  string `json:"status"`
			Notes   string `json:"notes"`
			Profile string `json:"profile"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		return body.Status, body.Notes, body.Profile
	}
	_ = r.ParseForm()
	return r.FormValue("status"), r.FormValue("notes"), r.FormValue("profile")
}
