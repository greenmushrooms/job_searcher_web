package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
	"github.com/greenmushrooms/job_searcher_web/api/internal/finalizations"
	"github.com/greenmushrooms/job_searcher_web/api/internal/jobs"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

type ResumeHandler struct {
	Jobs          *jobs.Repo
	Finalizations *finalizations.Repo
	DeepSeek      *deepseek.Client // may be nil if not configured
	Pool          *pgxpool.Pool    // for writing resume_drafted event directly
	Renderer      *render.Renderer // for /ui/* HTML fragments
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
	jobID := chi.URLParam(r, "id")
	var body draftRequestBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Profile == "" {
		body.Profile = "Slava"
	}

	draft, res, eventErr, herr := h.draftAndPersist(r.Context(), jobID, body.Profile)
	if herr != nil {
		writeErr(w, herr.status, herr.msg)
		return
	}

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
	Profile       string   `json:"profile"`
	KeptBulletIDs []string `json:"kept_bullet_ids"`
	ResumeVersion string   `json:"resume_version"` // optional; defaults to current
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

// ── HTML fragment routes ────────────────────────────────────────────────────

// draftFragmentView is the data shape consumed by web/templates/draft_fragment.html.
type draftFragmentView struct {
	JobID         string
	Profile       string
	NoDraft       bool
	Model         string
	ResumeVersion string
	DraftedAt     string
	CostUSD       float64
	KeptCount     int
	TotalCount    int
	Roles         []draftRoleView
}

type draftRoleView struct {
	RoleID      string
	RoleTitle   string
	RoleCompany string
	RoleDates   string
	Bullets     []draftBulletView
}

type draftBulletView struct {
	CompositeID string // role_id.bullet_id
	Text        string
	Keep        bool
	Reason      string
}

// DraftFragment handles GET /ui/jobs/{id}/draft?profile=…
// Reads the latest resume_drafted event for this (job, profile) and renders
// the bullet decisions as an htmx-swappable fragment. Renders an empty-state
// fragment with a "draft now" button if no event exists yet.
func (h *ResumeHandler) DraftFragment(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	profile := r.URL.Query().Get("profile")
	if profile == "" {
		profile = "Slava"
	}

	payload, draftedAt, err := h.latestDraftEvent(r.Context(), jobID, profile)
	if err != nil {
		http.Error(w, "load draft: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if payload == nil {
		h.Renderer.HTML(w, http.StatusOK, "draft_fragment", draftFragmentView{
			JobID:   jobID,
			Profile: profile,
			NoDraft: true,
		})
		return
	}

	res, err := resume.Load()
	if err != nil {
		http.Error(w, "resume load: "+err.Error(), http.StatusInternalServerError)
		return
	}
	view := buildDraftView(jobID, profile, draftedAt, payload, res)
	h.Renderer.HTML(w, http.StatusOK, "draft_fragment", view)
}

// DraftFragmentTrigger handles POST /ui/jobs/{id}/draft.
// Runs a fresh DeepSeek draft, persists the event, and renders the fragment.
// Powers the "draft now" button in the empty-state fragment.
func (h *ResumeHandler) DraftFragmentTrigger(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	profile := r.FormValue("profile")
	if profile == "" {
		profile = "Slava"
	}

	draft, res, _, herr := h.draftAndPersist(r.Context(), jobID, profile)
	if herr != nil {
		http.Error(w, herr.msg, herr.status)
		return
	}

	payload := draftEventPayload(draft, res)
	view := buildDraftView(jobID, profile, "just now", payload, res)
	h.Renderer.HTML(w, http.StatusOK, "draft_fragment", view)
}

// ── internals ───────────────────────────────────────────────────────────────

// draftedEventPayload is the JSON shape we store in application_events.payload
// for resume_drafted, mirrored here so both the writer and the fragment reader
// agree on field names.
type draftedEventPayload struct {
	PromptVersion string               `json:"prompt_version"`
	Model         string               `json:"model"`
	ResumeVersion string               `json:"resume_version"`
	CostUSD       float64              `json:"cost_usd"`
	BulletCount   int                  `json:"bullet_count"`
	Decisions     []deepseek.Decision  `json:"decisions"`
}

type handlerErr struct {
	status int
	msg    string
}

// draftAndPersist runs the LLM call and writes the resume_drafted event.
// Returns (draft, resume, eventErr, handlerErr). handlerErr is non-nil when
// the request itself should fail; eventErr is non-nil when the draft
// succeeded but the audit-trail insert failed (caller decides how to surface).
func (h *ResumeHandler) draftAndPersist(ctx context.Context, jobID, profile string) (*deepseek.DraftResult, *resume.Resume, error, *handlerErr) {
	if h.DeepSeek == nil {
		return nil, nil, nil, &handlerErr{http.StatusServiceUnavailable, "DeepSeek not configured (set DEEPSEEK_API_KEY)"}
	}

	job, err := h.Jobs.Get(ctx, jobID)
	if err != nil {
		return nil, nil, nil, &handlerErr{http.StatusInternalServerError, "job lookup: " + err.Error()}
	}
	if job == nil {
		return nil, nil, nil, &handlerErr{http.StatusNotFound, "job not found"}
	}
	if job.Description == nil || *job.Description == "" {
		return nil, nil, nil, &handlerErr{http.StatusBadRequest, "job has no description to tailor against"}
	}

	res, err := resume.Load()
	if err != nil {
		return nil, nil, nil, &handlerErr{http.StatusInternalServerError, "resume load: " + err.Error()}
	}

	draft, err := h.DeepSeek.Draft(ctx, *job.Description, res.Bullets)
	if err != nil {
		return nil, nil, nil, &handlerErr{http.StatusBadGateway, "deepseek: " + err.Error()}
	}

	payload, _ := json.Marshal(map[string]any{
		"prompt_version":    draft.PromptVersion,
		"model":             draft.Model,
		"resume_version":    res.Version,
		"prompt_tokens":     draft.Usage.PromptTokens,
		"completion_tokens": draft.Usage.CompletionTokens,
		"total_tokens":      draft.Usage.TotalTokens,
		"cost_usd":          draft.Usage.CostUSD,
		"bullet_count":      len(res.Bullets),
		"decisions":         draft.Decisions,
	})
	_, eventErr := h.Pool.Exec(ctx, `
        INSERT INTO web.application_events (sys_profile, job_id, event_type, payload)
        VALUES ($1, $2, 'resume_drafted', $3::jsonb)
    `, profile, jobID, string(payload))

	return draft, res, eventErr, nil
}

// latestDraftEvent fetches the most recent resume_drafted payload for (job,
// profile). Returns (nil, "", nil) when none exist.
func (h *ResumeHandler) latestDraftEvent(ctx context.Context, jobID, profile string) (*draftedEventPayload, string, error) {
	var raw []byte
	var createdAt string
	err := h.Pool.QueryRow(ctx, `
        SELECT payload, created_at::text
        FROM web.application_events
        WHERE event_type='resume_drafted' AND job_id=$1 AND sys_profile=$2
        ORDER BY created_at DESC
        LIMIT 1
    `, jobID, profile).Scan(&raw, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var p draftedEventPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, "", fmt.Errorf("parse payload: %w", err)
	}
	return &p, createdAt, nil
}

// draftEventPayload synthesises the same shape latestDraftEvent would have
// returned, for the just-drafted case where we already have the result in
// hand and don't need to round-trip through the DB.
func draftEventPayload(draft *deepseek.DraftResult, res *resume.Resume) *draftedEventPayload {
	return &draftedEventPayload{
		PromptVersion: draft.PromptVersion,
		Model:         draft.Model,
		ResumeVersion: res.Version,
		CostUSD:       draft.Usage.CostUSD,
		BulletCount:   len(res.Bullets),
		Decisions:     draft.Decisions,
	}
}

// buildDraftView joins decisions with bullet metadata so the template only
// has to iterate. Decisions whose bullet IDs no longer exist in the current
// resume are skipped — happens when resume_htmx removed/renamed a bullet
// after the draft was created.
func buildDraftView(jobID, profile, draftedAt string, p *draftedEventPayload, res *resume.Resume) draftFragmentView {
	view := draftFragmentView{
		JobID:         jobID,
		Profile:       profile,
		Model:         p.Model,
		ResumeVersion: p.ResumeVersion,
		DraftedAt:     draftedAt,
		CostUSD:       p.CostUSD,
		TotalCount:    len(p.Decisions),
	}

	// Index decisions by composite ID so we can render in resume order (which
	// is what the user is used to seeing in the resume_htmx UI), not LLM order.
	byID := make(map[string]deepseek.Decision, len(p.Decisions))
	for _, d := range p.Decisions {
		byID[d.RoleID+"."+d.BulletID] = d
		if d.Keep {
			view.KeptCount++
		}
	}

	roleIdx := map[string]int{}
	for _, b := range res.Bullets {
		d, ok := byID[b.CompositeID()]
		if !ok {
			continue
		}
		idx, seen := roleIdx[b.RoleID]
		if !seen {
			view.Roles = append(view.Roles, draftRoleView{
				RoleID:      b.RoleID,
				RoleTitle:   b.RoleTitle,
				RoleCompany: b.RoleCompany,
				RoleDates:   b.RoleDates,
			})
			idx = len(view.Roles) - 1
			roleIdx[b.RoleID] = idx
		}
		view.Roles[idx].Bullets = append(view.Roles[idx].Bullets, draftBulletView{
			CompositeID: b.CompositeID(),
			Text:        b.Text,
			Keep:        d.Keep,
			Reason:      d.Reason,
		})
	}

	// Stable role order is preserved by resume iteration, but if a decision
	// references a role that no longer exists in resume_data.json (whole role
	// retired), surface those bullets in a synthetic "removed roles" group at
	// the end rather than dropping them silently.
	missingByRole := map[string][]draftBulletView{}
	for _, d := range p.Decisions {
		cid := d.RoleID + "." + d.BulletID
		if res.Lookup(cid) != nil {
			continue
		}
		missingByRole[d.RoleID] = append(missingByRole[d.RoleID], draftBulletView{
			CompositeID: cid,
			Text:        "(bullet removed from resume_data.json)",
			Keep:        d.Keep,
			Reason:      d.Reason,
		})
	}
	if len(missingByRole) > 0 {
		// Deterministic ordering for the "missing" section.
		keys := make([]string, 0, len(missingByRole))
		for k := range missingByRole {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			view.Roles = append(view.Roles, draftRoleView{
				RoleID:    k,
				RoleTitle: "(retired role: " + k + ")",
				Bullets:   missingByRole[k],
			})
		}
	}

	return view
}
