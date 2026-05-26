package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
	"github.com/greenmushrooms/job_searcher_web/api/internal/finalizations"
	"github.com/greenmushrooms/job_searcher_web/api/internal/jobs"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

type ResumeHandler struct {
	Jobs           *jobs.Repo
	Finalizations  *finalizations.Repo
	DeepSeek       *deepseek.Client // may be nil if not configured
	Pool           *pgxpool.Pool    // for writing resume_drafted event directly
}

type draftRequestBody struct {
	Profile string `json:"profile"`
}

type draftResponse struct {
	Decisions     []deepseek.Decision `json:"decisions"`
	Usage         deepseek.Usage      `json:"usage"`
	Model         string              `json:"model"`
	PromptVersion string              `json:"prompt_version"`
	ResumeVersion string              `json:"resume_version"`
	BulletCount   int                 `json:"bullet_count"`
}

// Draft handles POST /api/v1/jobs/{id}/draft-resume.
// Pulls the job description, loads the active bullet pool, calls DeepSeek,
// writes a resume_drafted event with full token/cost telemetry, returns
// per-bullet decisions as JSON.
func (h *ResumeHandler) Draft(w http.ResponseWriter, r *http.Request) {
	if h.DeepSeek == nil {
		writeErr(w, http.StatusServiceUnavailable, "DeepSeek not configured (set DEEPSEEK_API_KEY)")
		return
	}

	jobID := chi.URLParam(r, "id")
	var body draftRequestBody
	_ = json.NewDecoder(r.Body).Decode(&body) // body is optional
	if body.Profile == "" {
		body.Profile = "Slava"
	}

	job, err := h.Jobs.Get(r.Context(), jobID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "job lookup: "+err.Error())
		return
	}
	if job == nil {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	if job.Description == nil || *job.Description == "" {
		writeErr(w, http.StatusBadRequest, "job has no description to tailor against")
		return
	}

	res, err := resume.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "resume load: "+err.Error())
		return
	}

	draft, err := h.DeepSeek.Draft(r.Context(), *job.Description, res.Bullets)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "deepseek: "+err.Error())
		return
	}

	// Append resume_drafted event. Failure here is a real problem (audit
	// trail) but shouldn't block returning the draft to the user — log via
	// 500 only if the draft itself fails. Here we 207-style: include event
	// error in the response body if it occurred, but still 200.
	payload, _ := json.Marshal(map[string]any{
		"prompt_version":   draft.PromptVersion,
		"model":            draft.Model,
		"resume_version":   res.Version,
		"prompt_tokens":    draft.Usage.PromptTokens,
		"completion_tokens": draft.Usage.CompletionTokens,
		"total_tokens":     draft.Usage.TotalTokens,
		"cost_usd":         draft.Usage.CostUSD,
		"bullet_count":     len(res.Bullets),
		"decisions":        draft.Decisions,
	})
	_, eventErr := h.Pool.Exec(r.Context(), `
        INSERT INTO web.application_events (sys_profile, job_id, event_type, payload)
        VALUES ($1, $2, 'resume_drafted', $3::jsonb)
    `, body.Profile, jobID, string(payload))

	resp := draftResponse{
		Decisions:     draft.Decisions,
		Usage:         draft.Usage,
		Model:         draft.Model,
		PromptVersion: draft.PromptVersion,
		ResumeVersion: res.Version,
		BulletCount:   len(res.Bullets),
	}
	if eventErr != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"draft":       resp,
			"event_error": eventErr.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

type finalizeRequestBody struct {
	Profile        string   `json:"profile"`
	KeptBulletIDs  []string `json:"kept_bullet_ids"`
	ResumeVersion  string   `json:"resume_version"` // optional; defaults to current
}

// Finalize handles POST /api/v1/jobs/{id}/finalize-resume.
// Persists the user's final bullet selection (which may differ from the
// LLM's draft) and writes a resume_finalized event.
func (h *ResumeHandler) Finalize(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	var body finalizeRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Profile == "" {
		body.Profile = "Slava"
	}
	if body.KeptBulletIDs == nil {
		writeErr(w, http.StatusBadRequest, "kept_bullet_ids required (use [] to keep nothing)")
		return
	}

	resumeVersion := body.ResumeVersion
	if resumeVersion == "" {
		res, err := resume.Load()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "resume load: "+err.Error())
			return
		}
		resumeVersion = res.Version
	}

	exists, err := h.jobExists(r.Context(), jobID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !exists {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}

	saved, err := h.Finalizations.Save(r.Context(), jobID, body.Profile, resumeVersion, body.KeptBulletIDs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *ResumeHandler) jobExists(ctx context.Context, jobID string) (bool, error) {
	var exists bool
	err := h.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM public.jobspy_jobs WHERE id = $1)`,
		jobID,
	).Scan(&exists)
	return exists, err
}
