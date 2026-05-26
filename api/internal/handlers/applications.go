package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/applications"
)

type ApplicationsHandler struct {
	Repo *applications.Repo
}

type setStatusBody struct {
	Status  string  `json:"status"`
	Notes   *string `json:"notes"`
	Profile string  `json:"profile"`
}

func (h *ApplicationsHandler) SetStatus(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	var body setStatusBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Profile == "" {
		body.Profile = "Slava"
	}

	app, err := h.Repo.Upsert(r.Context(), jobID, body.Profile, body.Status, body.Notes)
	switch {
	case errors.Is(err, applications.ErrInvalidStatus):
		validList := make([]string, 0, len(applications.ValidStatuses))
		for s := range applications.ValidStatuses {
			validList = append(validList, s)
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "invalid status",
			"valid":  validList,
		})
		return
	case errors.Is(err, applications.ErrJobNotFound):
		writeErr(w, http.StatusNotFound, "job not found")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, app)
}
