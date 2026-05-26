package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/jobs"
)

type JobsHandler struct {
	Repo *jobs.Repo
}

func (h *JobsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := jobs.ListParams{
		Profile:   q.Get("profile"),
		MinScore:  6.9,
		Limit:     50,
		Offset:    0,
		From:      q.Get("from"),
		To:        q.Get("to"),
		DateField: q.Get("date_field"),
	}
	if p.Profile == "" {
		p.Profile = "Slava"
	}
	if p.DateField == "" {
		p.DateField = "eval"
	}
	if p.DateField != "eval" && p.DateField != "posted" {
		writeErr(w, http.StatusBadRequest, "date_field must be 'eval' or 'posted'")
		return
	}
	if s := q.Get("min_score"); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid min_score")
			return
		}
		p.MinScore = v
	}
	if s := q.Get("limit"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if v < 1 {
			v = 1
		} else if v > 200 {
			v = 200
		}
		p.Limit = v
	}
	if s := q.Get("offset"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 0 {
			writeErr(w, http.StatusBadRequest, "invalid offset")
			return
		}
		p.Offset = v
	}

	out, err := h.Repo.List(r.Context(), p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out, "count": len(out)})
}

func (h *JobsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	job, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if job == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *JobsHandler) Profiles(w http.ResponseWriter, r *http.Request) {
	out, err := h.Repo.Profiles(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

func (h *JobsHandler) About(w http.ResponseWriter, r *http.Request) {
	profiles, _ := h.Repo.Profiles(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"name":               "job_searcher_web",
		"version":            "1",
		"description":        "User-facing API on top of the job_searcher_2 pipeline. Reads public.* (pipeline), writes web.* (user state).",
		"profiles":           profiles,
		"default_min_score":  6.9,
		"endpoints": []map[string]string{
			{"method": "GET", "path": "/api/v1/about"},
			{"method": "GET", "path": "/api/v1/profiles"},
			{"method": "GET", "path": "/api/v1/jobs"},
			{"method": "GET", "path": "/api/v1/jobs/{id}"},
			{"method": "POST", "path": "/api/v1/jobs/{id}/status"},
		},
	})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
