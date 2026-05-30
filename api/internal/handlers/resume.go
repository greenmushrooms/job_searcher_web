package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
	"github.com/greenmushrooms/job_searcher_web/api/internal/finalizations"
	"github.com/greenmushrooms/job_searcher_web/api/internal/jobs"
	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

type ResumeHandler struct {
	Jobs          *jobs.Repo
	Finalizations *finalizations.Repo
	Resumes       *resume.Repo     // writes to the canonical resume (left editor)
	DeepSeek      *deepseek.Client // may be nil if not configured
	Pool          *pgxpool.Pool    // for writing resume_drafted event directly
	Renderer      *render.Renderer // for /ui/* HTML fragments
}

type draftRequestBody struct {
	Profile string `json:"profile"`
}

type draftResponse struct {
	Removals      []deepseek.Removal `json:"removals"`
	Rewrites      []deepseek.Rewrite `json:"rewrites"`
	Usage         deepseek.Usage     `json:"usage"`
	Model         string             `json:"model"`
	PromptVersion string             `json:"prompt_version"`
	ResumeVersion string             `json:"resume_version"`
	BulletCount   int                `json:"bullet_count"`
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
		body.Profile = profiles.Default
	} else if !profiles.Valid(r.Context(), body.Profile) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown profile", "valid": profiles.Known(r.Context())})
		return
	}

	draft, res, eventErr, herr := h.draftAndPersist(r.Context(), jobID, body.Profile, resume.DefaultTemplateID)
	if herr != nil {
		writeErr(w, herr.status, herr.msg)
		return
	}

	resp := draftResponse{
		Removals:      draft.Removals,
		Rewrites:      draft.Rewrites,
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
		body.Profile = profiles.Default
	} else if !profiles.Valid(r.Context(), body.Profile) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown profile", "valid": profiles.Known(r.Context())})
		return
	}
	if body.KeptBulletIDs == nil {
		writeErr(w, http.StatusBadRequest, "kept_bullet_ids required (use [] to keep nothing)")
		return
	}

	resumeVersion := body.ResumeVersion
	if resumeVersion == "" {
		res, err := resume.Load(r.Context(), h.Pool)
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

	removals := h.latestRemovalsJSON(r.Context(), jobID, body.Profile)
	saved, err := h.Finalizations.Save(r.Context(), finalizations.SaveInput{
		JobID:         jobID,
		SysProfile:    body.Profile,
		ResumeVersion: resumeVersion,
		TemplateID:    resume.DefaultTemplateID,
		KeptBulletIDs: body.KeptBulletIDs,
		Removals:      removals,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// latestRemovalsJSON snapshots the LLM removal diff from the most recent draft
// event so a generated jobs_resume row self-describes its tailoring. Returns
// nil (stored as []) when there's no draft to snapshot.
func (h *ResumeHandler) latestRemovalsJSON(ctx context.Context, jobID, profile string) []byte {
	payload, _, err := h.latestDraftEvent(ctx, jobID, profile)
	if err != nil || payload == nil {
		return nil
	}
	b, _ := json.Marshal(payload.effectiveRemovals())
	return b
}

func (h *ResumeHandler) jobExists(ctx context.Context, jobID string) (bool, error) {
	var exists bool
	err := h.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM public.jobspy_jobs WHERE id = $1)`,
		jobID,
	).Scan(&exists)
	return exists, err
}

// generateResultView feeds web/templates/generate_result.html.
type generateResultView struct {
	JobID         string
	Profile       string
	TemplateID    string
	KeptCount     int
	ResumeVersion string
	GeneratedAt   string
}

// finalBullet is one bullet in the generated resume's stored snapshot. source
// records where the final text came from so the row self-describes its
// tailoring: canonical (unchanged), manual (user-edited), or ai (accepted
// rewrite).
type finalBullet struct {
	RoleID   string `json:"role_id"`
	BulletID string `json:"bullet_id"`
	Text     string `json:"text"`
	Source   string `json:"source"`
}

// GenerateResume handles POST /ui/jobs/{id}/generate — the right-hand "Generate"
// button. The checkbox form posts the kept bullet IDs (standard form encoding,
// repeated kept_bullet_ids); we snapshot the LLM removals, upsert jobs_resume,
// and swap in a confirmation fragment. PDF rendering arrives in a later slice.
func (h *ResumeHandler) GenerateResume(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))
	templateID := templateOrDefault(r.FormValue("template"))
	kept := r.Form["kept_bullet_ids"]
	if kept == nil {
		kept = []string{}
	}

	res, err := resume.LoadTemplate(r.Context(), h.Pool, profile, templateID)
	if err != nil {
		http.Error(w, "resume load: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resumeVersion := r.FormValue("resume_version")
	if resumeVersion == "" {
		resumeVersion = res.Version
	}

	exists, err := h.jobExists(r.Context(), jobID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	bullets := buildFinalBullets(kept, r, res, h.latestRewrites(r.Context(), jobID, profile))
	bulletsJSON, _ := json.Marshal(bullets)
	removals := h.latestRemovalsJSON(r.Context(), jobID, profile)

	saved, err := h.Finalizations.Save(r.Context(), finalizations.SaveInput{
		JobID:         jobID,
		SysProfile:    profile,
		ResumeVersion: resumeVersion,
		TemplateID:    templateID,
		KeptBulletIDs: kept,
		Removals:      removals,
		Bullets:       bulletsJSON,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "generate_result", generateResultView{
		JobID:         jobID,
		Profile:       profile,
		TemplateID:    templateID,
		KeptCount:     len(saved.KeptBulletIDs),
		ResumeVersion: saved.ResumeVersion,
		GeneratedAt:   saved.GeneratedAt,
	})
}

// buildFinalBullets assembles the per-bullet snapshot for the kept selection,
// reading each bullet's final text from the form (text_<composite>) and
// tagging its source by comparing against the canonical pool text and the
// latest AI rewrite.
func buildFinalBullets(kept []string, r *http.Request, res *resume.Resume, rewrites map[string]string) []finalBullet {
	canon := make(map[string]string, len(res.Bullets))
	for _, b := range res.Bullets {
		canon[b.CompositeID()] = b.Text
	}
	out := make([]finalBullet, 0, len(kept))
	for _, cid := range kept {
		dot := strings.IndexByte(cid, '.')
		if dot < 0 {
			continue
		}
		text := r.FormValue("text_" + cid)
		if strings.TrimSpace(text) == "" {
			text = canon[cid]
		}
		source := "manual"
		switch {
		case text == canon[cid]:
			source = "canonical"
		case rewrites[cid] != "" && text == rewrites[cid]:
			source = "ai"
		}
		out = append(out, finalBullet{
			RoleID:   cid[:dot],
			BulletID: cid[dot+1:],
			Text:     text,
			Source:   source,
		})
	}
	return out
}

// latestRewrites returns composite-ID -> new text from the most recent draft
// event, used to tag accepted AI rewrites at generate time.
func (h *ResumeHandler) latestRewrites(ctx context.Context, jobID, profile string) map[string]string {
	payload, _, err := h.latestDraftEvent(ctx, jobID, profile)
	if err != nil || payload == nil {
		return nil
	}
	m := make(map[string]string, len(payload.Rewrites))
	for _, rw := range payload.Rewrites {
		m[rw.RoleID+"."+rw.BulletID] = rw.NewText
	}
	return m
}

// templateOrDefault maps an empty template param to the virtual Default.
func templateOrDefault(t string) string {
	if t == "" {
		return resume.DefaultTemplateID
	}
	return t
}

// ── HTML fragment routes ────────────────────────────────────────────────────

// draftFragmentView is the data shape consumed by web/templates/draft_fragment.html.
type draftFragmentView struct {
	JobID         string
	Profile       string
	TemplateID    string
	NoDraft       bool
	Model         string
	ResumeVersion string
	DraftedAt     string
	CostUSD       float64
	KeptCount     int
	RewriteCount  int
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
	CompositeID   string // role_id.bullet_id
	Text          string // current canonical text
	Keep          bool
	Reason        string // removal reason, when the LLM flagged it for drop
	NewText       string // AI-suggested rewrite; empty when none
	RewriteReason string
}

// DraftFragment handles GET /ui/jobs/{id}/draft?profile=…
// Reads the latest resume_drafted event for this (job, profile) and renders
// the bullet decisions as an htmx-swappable fragment. Renders an empty-state
// fragment with a "draft now" button if no event exists yet.
func (h *ResumeHandler) DraftFragment(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))

	templateID := templateOrDefault(r.URL.Query().Get("template"))

	payload, draftedAt, err := h.latestDraftEvent(r.Context(), jobID, profile)
	if err != nil {
		http.Error(w, "load draft: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if payload == nil {
		h.Renderer.HTML(w, http.StatusOK, "draft_fragment", draftFragmentView{
			JobID:      jobID,
			Profile:    profile,
			TemplateID: templateID,
			NoDraft:    true,
		})
		return
	}

	res, err := resume.LoadTemplate(r.Context(), h.Pool, profile, templateID)
	if err != nil {
		http.Error(w, "resume load: "+err.Error(), http.StatusInternalServerError)
		return
	}
	view := buildDraftView(jobID, profile, draftedAt, payload, res)
	view.TemplateID = templateID
	h.Renderer.HTML(w, http.StatusOK, "draft_fragment", view)
}

// DraftFragmentTrigger handles POST /ui/jobs/{id}/draft.
// Runs a fresh DeepSeek draft, persists the event, and renders the fragment.
// Powers the "draft now" button in the empty-state fragment.
func (h *ResumeHandler) DraftFragmentTrigger(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))
	templateID := templateOrDefault(r.FormValue("template"))

	draft, res, _, herr := h.draftAndPersist(r.Context(), jobID, profile, templateID)
	if herr != nil {
		http.Error(w, herr.msg, herr.status)
		return
	}

	payload := draftEventPayload(draft, res)
	view := buildDraftView(jobID, profile, "just now", payload, res)
	view.TemplateID = templateID
	h.Renderer.HTML(w, http.StatusOK, "draft_fragment", view)
}

// ── internals ───────────────────────────────────────────────────────────────

// draftedEventPayload is the JSON shape we store in application_events.payload
// for resume_drafted, mirrored here so both the writer and the fragment reader
// agree on field names.
type draftedEventPayload struct {
	PromptVersion string             `json:"prompt_version"`
	Model         string             `json:"model"`
	ResumeVersion string             `json:"resume_version"`
	CostUSD       float64            `json:"cost_usd"`
	BulletCount   int                `json:"bullet_count"`
	Removals      []deepseek.Removal `json:"removals"`
	Rewrites      []deepseek.Rewrite `json:"rewrites"`
	// LegacyDecisions reads pre-v2 events that stored per-bullet keep/drop
	// decisions instead of removals. effectiveRemovals() folds them in.
	LegacyDecisions []legacyDecision `json:"decisions,omitempty"`
}

// legacyDecision is the old (pre-v2) per-bullet payload shape. Kept only so
// drafts created before the removals-only reframe still render.
type legacyDecision struct {
	RoleID   string `json:"role_id"`
	BulletID string `json:"bullet_id"`
	Keep     bool   `json:"keep"`
	Reason   string `json:"reason"`
}

// effectiveRemovals returns the removal set, converting a legacy decisions
// payload (dropped = keep:false) when the event predates the v2 schema.
func (p *draftedEventPayload) effectiveRemovals() []deepseek.Removal {
	if len(p.Removals) > 0 || len(p.LegacyDecisions) == 0 {
		return p.Removals
	}
	var out []deepseek.Removal
	for _, d := range p.LegacyDecisions {
		if !d.Keep {
			out = append(out, deepseek.Removal{RoleID: d.RoleID, BulletID: d.BulletID, Reason: d.Reason})
		}
	}
	return out
}

type handlerErr struct {
	status int
	msg    string
}

// draftAndPersist runs the LLM call and writes the resume_drafted event.
// Returns (draft, resume, eventErr, handlerErr). handlerErr is non-nil when
// the request itself should fail; eventErr is non-nil when the draft
// succeeded but the audit-trail insert failed (caller decides how to surface).
func (h *ResumeHandler) draftAndPersist(ctx context.Context, jobID, profile, templateID string) (*deepseek.DraftResult, *resume.Resume, error, *handlerErr) {
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

	res, err := resume.LoadTemplate(ctx, h.Pool, profile, templateID)
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
		"removals":          draft.Removals,
		"rewrites":          draft.Rewrites,
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
		Removals:      draft.Removals,
		Rewrites:      draft.Rewrites,
	}
}

// buildDraftView renders the full current resume and overlays the draft's
// removals as a diff: every bullet is kept (checked) unless the LLM flagged it
// for removal, in which case it's unchecked and shows the reason. Removals that
// point at bullets no longer in the resume are simply ignored — the resume is
// the source of truth, the removals are just a thin overlay.
func buildDraftView(jobID, profile, draftedAt string, p *draftedEventPayload, res *resume.Resume) draftFragmentView {
	removed := make(map[string]string) // composite ID -> removal reason
	for _, rm := range p.effectiveRemovals() {
		removed[rm.RoleID+"."+rm.BulletID] = rm.Reason
	}
	rewritten := make(map[string]deepseek.Rewrite) // composite ID -> rewrite
	for _, rw := range p.Rewrites {
		rewritten[rw.RoleID+"."+rw.BulletID] = rw
	}

	view := draftFragmentView{
		JobID:         jobID,
		Profile:       profile,
		Model:         p.Model,
		ResumeVersion: p.ResumeVersion,
		DraftedAt:     draftedAt,
		CostUSD:       p.CostUSD,
		TotalCount:    len(res.Bullets),
	}

	roleIdx := map[string]int{}
	for _, b := range res.Bullets {
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
		reason, isRemoved := removed[b.CompositeID()]
		if !isRemoved {
			view.KeptCount++
		}
		bv := draftBulletView{
			CompositeID: b.CompositeID(),
			Text:        b.Text,
			Keep:        !isRemoved,
			Reason:      reason,
		}
		// A removed bullet's rewrite (if any) is irrelevant — it's dropped.
		if rw, ok := rewritten[b.CompositeID()]; ok && !isRemoved {
			bv.NewText = rw.NewText
			bv.RewriteReason = rw.Reason
			view.RewriteCount++
		}
		view.Roles[idx].Bullets = append(view.Roles[idx].Bullets, bv)
	}

	return view
}
