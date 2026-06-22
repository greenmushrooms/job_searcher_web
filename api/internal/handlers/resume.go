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

	"github.com/greenmushrooms/job_searcher_web/api/internal/coverletters"
	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
	"github.com/greenmushrooms/job_searcher_web/api/internal/finalizations"
	"github.com/greenmushrooms/job_searcher_web/api/internal/jobs"
	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resumemaster"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resumesuggest"
	"github.com/greenmushrooms/job_searcher_web/api/internal/templates"
)

type ResumeHandler struct {
	Jobs          *jobs.Repo
	Finalizations *finalizations.Repo
	CoverLetters  *coverletters.Repo
	Master        *resumemaster.Repo // permanent master résumé markdown (diff lab)
	Resumes       *resume.Repo       // writes to the canonical resume (left editor)
	Templates     *templates.Repo    // reusable resume variants
	DeepSeek      *deepseek.Client   // may be nil if not configured
	Pool          *pgxpool.Pool      // for writing resume_drafted event directly
	Renderer      *render.Renderer   // for /ui/* HTML fragments
}

type draftRequestBody struct {
	Profile string `json:"profile"`
}

type draftResponse struct {
	Removals          []deepseek.Removal          `json:"removals"`
	Rewrites          []deepseek.Rewrite          `json:"rewrites"`
	EducationRemovals []deepseek.EducationRemoval `json:"education_removals"`
	Usage             deepseek.Usage              `json:"usage"`
	Model             string                      `json:"model"`
	PromptVersion     string                      `json:"prompt_version"`
	ResumeVersion     string                      `json:"resume_version"`
	BulletCount       int                         `json:"bullet_count"`
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
		Removals:          draft.Removals,
		Rewrites:          draft.Rewrites,
		EducationRemovals: draft.EducationRemovals,
		Usage:             draft.Usage,
		Model:             draft.Model,
		PromptVersion:     draft.PromptVersion,
		ResumeVersion:     res.Version,
		BulletCount:       len(res.Bullets),
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
	ResumeVersion string
	GeneratedAt   string
	Status        string      // current review status — hides the "Mark as applied" nudge once applied
	Row           *jobRowView // OOB list-row refresh so the 📄 badge shows up immediately
}

// saveResumeFromForm persists the working-copy markdown from the two-pane
// form to jobs_resume, snapshotting the LLM removals for audit. Shared by
// GenerateResume (htmx confirmation) and GeneratePDF (save + stream PDF).
func (h *ResumeHandler) saveResumeFromForm(r *http.Request, jobID string) (*finalizations.Finalization, string, string, *handlerErr) {
	if err := r.ParseForm(); err != nil {
		return nil, "", "", &handlerErr{http.StatusBadRequest, "bad form"}
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))
	templateID := templateOrDefault(r.FormValue("template"))
	markdown := r.FormValue("markdown")
	if strings.TrimSpace(markdown) == "" {
		return nil, profile, templateID, &handlerErr{http.StatusBadRequest, "empty resume markdown"}
	}

	resumeVersion := r.FormValue("resume_version")
	if resumeVersion == "" {
		if res, err := resume.LoadTemplate(r.Context(), h.Pool, profile, templateID); err == nil {
			resumeVersion = res.Version
		}
	}

	exists, err := h.jobExists(r.Context(), jobID)
	if err != nil {
		return nil, profile, templateID, &handlerErr{http.StatusInternalServerError, err.Error()}
	}
	if !exists {
		return nil, profile, templateID, &handlerErr{http.StatusNotFound, "job not found"}
	}

	removals := h.latestRemovalsJSON(r.Context(), jobID, profile)
	saved, err := h.Finalizations.Save(r.Context(), finalizations.SaveInput{
		JobID:         jobID,
		SysProfile:    profile,
		ResumeVersion: resumeVersion,
		TemplateID:    templateID,
		Removals:      removals,
		Markdown:      markdown,
	})
	if err != nil {
		return nil, profile, templateID, &handlerErr{http.StatusInternalServerError, err.Error()}
	}
	return saved, profile, templateID, nil
}

// GenerateResume handles POST /ui/jobs/{id}/generate — the "Save résumé"
// button. The two-pane form posts the (possibly hand-edited) resume markdown
// from the left textarea; we persist it to jobs_resume and swap in a
// confirmation fragment with a Preview PDF link.
func (h *ResumeHandler) GenerateResume(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	saved, profile, templateID, herr := h.saveResumeFromForm(r, jobID)
	if herr != nil {
		http.Error(w, herr.msg, herr.status)
		return
	}
	view := generateResultView{
		JobID:         jobID,
		Profile:       profile,
		TemplateID:    templateID,
		ResumeVersion: saved.ResumeVersion,
		GeneratedAt:   saved.GeneratedAt,
	}
	// Refresh the list row out-of-band so the 📄 badge appears without a
	// list reload; best-effort, the save already succeeded.
	if j, err := h.Jobs.Get(r.Context(), jobID); err == nil && j != nil {
		row := toRowView(*j, profile)
		row.HasResume = true // the row we just upserted
		row.OOB = true
		view.Row = &row
		view.Status = row.Status
	}
	h.Renderer.HTML(w, http.StatusOK, "generate_result", view)
}

// templateOrDefault maps an empty template param to the virtual Default.
func templateOrDefault(t string) string {
	if t == "" {
		return resume.DefaultTemplateID
	}
	return t
}

// ── resume templates (Slice B/C) ─────────────────────────────────────────────

// templateOpt is one entry in the template dropdown.
type templateOpt struct {
	ID        string
	Name      string
	IsDefault bool
}

// resumeControlsView feeds web/templates/resume_controls.html — the template
// dropdown above the per-job draft panel.
type resumeControlsView struct {
	JobID      string
	Profile    string
	TemplateID string
	Templates  []templateOpt
	OOB        bool
}

// resumeControls builds the dropdown: a synthetic "Full résumé" (the whole
// canonical pool) followed by the profile's stored templates. When templateID
// is empty it resolves to the profile's default template, else the full pool.
func resumeControls(ctx context.Context, repo *templates.Repo, jobID, profile, templateID string, oob bool) (resumeControlsView, error) {
	list, err := repo.List(ctx, profile)
	if err != nil {
		return resumeControlsView{}, err
	}
	opts := make([]templateOpt, 0, len(list)+1)
	opts = append(opts, templateOpt{ID: resume.DefaultTemplateID, Name: "Full résumé (all bullets)"})
	defaultID := ""
	for _, t := range list {
		opts = append(opts, templateOpt{ID: t.ID, Name: t.Name, IsDefault: t.IsDefault})
		if t.IsDefault {
			defaultID = t.ID
		}
	}
	if templateID == "" {
		if defaultID != "" {
			templateID = defaultID
		} else {
			templateID = resume.DefaultTemplateID
		}
	}
	return resumeControlsView{
		JobID:      jobID,
		Profile:    profile,
		TemplateID: templateID,
		Templates:  opts,
		OOB:        oob,
	}, nil
}

type templateSavedView struct {
	JobID      string
	Profile    string
	TemplateID string
	Name       string
	Controls   resumeControlsView
}

// SaveTemplate handles POST /ui/jobs/{id}/save-template — snapshot the current
// left-pane markdown into a new reusable template. In the markdown-centric flow
// a template is a free-form resume document, not a structured bullet selection.
func (h *ResumeHandler) SaveTemplate(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "template name required", http.StatusBadRequest)
		return
	}
	markdown := r.FormValue("markdown")
	if strings.TrimSpace(markdown) == "" {
		http.Error(w, "empty resume markdown", http.StatusBadRequest)
		return
	}

	id, err := h.Templates.SaveMarkdown(r.Context(), profile, name, markdown)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.templateSavedEvent(r.Context(), profile, jobID, id, name)

	h.renderTemplateSaved(w, r, jobID, profile, id, name)
}

// ReplaceTemplate handles POST /ui/jobs/{id}/replace-template — overwrite an
// existing template's markdown with the current left-pane markdown. The target
// template id comes from the actions-row dropdown (form field target_template).
func (h *ResumeHandler) ReplaceTemplate(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))
	id := r.FormValue("target_template")
	if id == "" || id == resume.DefaultTemplateID {
		http.Error(w, "choose a template to replace", http.StatusBadRequest)
		return
	}
	markdown := r.FormValue("markdown")
	if strings.TrimSpace(markdown) == "" {
		http.Error(w, "empty resume markdown", http.StatusBadRequest)
		return
	}
	if err := h.Templates.ReplaceMarkdown(r.Context(), profile, id, markdown); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := id
	if list, err := h.Templates.List(r.Context(), profile); err == nil {
		for _, t := range list {
			if t.ID == id {
				name = t.Name
			}
		}
	}
	h.templateSavedEvent(r.Context(), profile, jobID, id, name)
	h.renderTemplateSaved(w, r, jobID, profile, id, name)
}

func (h *ResumeHandler) templateSavedEvent(ctx context.Context, profile, jobID, id, name string) {
	payload, _ := json.Marshal(map[string]string{"template_id": id, "name": name})
	_, _ = h.Pool.Exec(ctx, `
        INSERT INTO web.application_events (sys_profile, job_id, event_type, payload)
        VALUES ($1, $2, 'resume_template_saved', $3::jsonb)
    `, profile, jobID, payload)
}

func (h *ResumeHandler) renderTemplateSaved(w http.ResponseWriter, r *http.Request, jobID, profile, id, name string) {
	controls, err := resumeControls(r.Context(), h.Templates, jobID, profile, id, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "template_saved", templateSavedView{
		JobID:      jobID,
		Profile:    profile,
		TemplateID: id,
		Name:       name,
		Controls:   controls,
	})
}

// ── template manager (Slice C) ───────────────────────────────────────────────

type templateManagerView struct {
	JobID     string
	Profile   string
	Templates []templates.Template
	// Controls, when set, is rendered as an OOB dropdown refresh after a
	// mutation so an open job's selector stays in sync.
	Controls *resumeControlsView
}

// TemplatesManager handles GET /ui/resume/templates — the manage panel (rename,
// delete, set default). The optional ?job= is carried so "back" can reload that
// job's draft.
func (h *ResumeHandler) TemplatesManager(w http.ResponseWriter, r *http.Request) {
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))
	jobID := r.URL.Query().Get("job")
	list, err := h.Templates.List(r.Context(), profile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "template_manager", templateManagerView{
		JobID:     jobID,
		Profile:   profile,
		Templates: list,
	})
}

func (h *ResumeHandler) RenameTemplate(w http.ResponseWriter, r *http.Request) {
	h.templateMutation(w, r, func(ctx context.Context, profile, id string) error {
		return h.Templates.Rename(ctx, profile, id, strings.TrimSpace(r.FormValue("name")))
	})
}

func (h *ResumeHandler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	h.templateMutation(w, r, func(ctx context.Context, profile, id string) error {
		return h.Templates.Delete(ctx, profile, id)
	})
}

func (h *ResumeHandler) SetDefaultTemplate(w http.ResponseWriter, r *http.Request) {
	h.templateMutation(w, r, func(ctx context.Context, profile, id string) error {
		return h.Templates.SetDefault(ctx, profile, id)
	})
}

// templateMutation runs a rename/delete/set-default then re-renders the manager,
// with an OOB dropdown refresh when a job is open.
func (h *ResumeHandler) templateMutation(w http.ResponseWriter, r *http.Request, mutate func(context.Context, string, string) error) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))
	id := chi.URLParam(r, "templateID")
	jobID := r.FormValue("job")
	if err := mutate(r.Context(), profile, id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	list, err := h.Templates.List(r.Context(), profile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	view := templateManagerView{JobID: jobID, Profile: profile, Templates: list}
	if jobID != "" {
		if controls, err := resumeControls(r.Context(), h.Templates, jobID, profile, "", true); err == nil {
			view.Controls = &controls
		}
	}
	h.Renderer.HTML(w, http.StatusOK, "template_manager", view)
}

// ── HTML fragment routes ────────────────────────────────────────────────────

// draftFragmentView is the data shape consumed by web/templates/draft_fragment.html.
// The left pane is the editable working-copy markdown (BaseMarkdown); the right
// pane is an editable copy of the AI-tailored markdown (TailoredMarkdown). The
// live diff between the two is rendered client-side by web/resume_diff.js.
type draftFragmentView struct {
	JobID                string
	Profile              string
	TemplateID           string
	TemplateName         string // display name for the selected template
	NoDraft              bool   // no AI draft yet → right pane shows the trigger
	BaseMarkdown         string // left textarea content
	TailoredMarkdown     string // payload for "Apply AI suggestions"
	HasSaved             bool   // a generated resume exists → show Preview PDF
	Model                string
	ResumeVersion        string
	DraftedAt            string
	CostUSD              float64
	RemovedCount         int
	RewriteCount         int
	EducationPrunedCount int
	TotalCount           int
	ReplaceTargets       []templateOpt // stored templates for the "Replace" dropdown
	// Density is the deterministic "too many points per job" check over the
	// canonical bullet pool — surfaced as a banner above the panes, independent
	// of any AI draft.
	Density resumesuggest.Report
}

// markdownFragment assembles the two-pane view. The right pane is an editable
// copy of the AI-tailored markdown; tailoredOverride, when set, replaces the
// freshly-computed tailored text with the user's hand-edited version (so their
// edits survive a re-render and the diff reflects them). The diff is always
// canonical-vs-(tailored or override). The left-pane base follows the
// precedence: explicit override (Apply AI) → saved job markdown → selected
// template markdown → canonical render.
func (h *ResumeHandler) markdownFragment(ctx context.Context, jobID, profile, templateID, baseOverride, tailoredOverride string, payload *draftedEventPayload, draftedAt string) (draftFragmentView, *handlerErr) {
	res, err := resume.LoadTemplate(ctx, h.Pool, profile, templateID)
	if err != nil {
		return draftFragmentView{}, &handlerErr{http.StatusInternalServerError, "resume load: " + err.Error()}
	}
	doc, err := resume.LoadDocument(ctx, h.Pool, profile)
	if err != nil {
		return draftFragmentView{}, &handlerErr{http.StatusInternalServerError, "load resume: " + err.Error()}
	}
	canonicalMD := resume.ToMarkdown(doc, res.Bullets)

	view := draftFragmentView{
		JobID:         jobID,
		Profile:       profile,
		TemplateID:    templateID,
		ResumeVersion: res.Version,
		TotalCount:    len(res.Bullets),
		NoDraft:       payload == nil,
		Density:       resumesuggest.Analyze(resumesuggest.FromBullets(res.Bullets), resumesuggest.DefaultLimits),
	}

	if payload != nil {
		removed := map[string]string{}
		for _, rm := range payload.effectiveRemovals() {
			removed[rm.RoleID+"."+rm.BulletID] = rm.Reason
		}
		rewritten := map[string]deepseek.Rewrite{}
		for _, rw := range payload.Rewrites {
			rewritten[rw.RoleID+"."+rw.BulletID] = rw
		}
		removedEdu := map[string]string{}
		for _, er := range payload.EducationRemovals {
			removedEdu[er.EducationID] = er.Reason
		}
		// Prune education only on the tailored (right) side; the working copy
		// (base) keeps the full education list.
		tailoredMD := resume.ToMarkdown(docWithEducationPruned(doc, removedEdu), tailoredBullets(res.Bullets, removed, rewritten))
		if strings.TrimSpace(tailoredOverride) != "" {
			tailoredMD = tailoredOverride
		}
		view.TailoredMarkdown = tailoredMD
		view.Model = payload.Model
		view.DraftedAt = draftedAt
		view.CostUSD = payload.CostUSD
		view.RemovedCount = len(removed)
		view.RewriteCount = len(rewritten)
		view.EducationPrunedCount = len(removedEdu)
		if payload.ResumeVersion != "" {
			view.ResumeVersion = payload.ResumeVersion
		}
	}

	saved, _ := h.Finalizations.Get(ctx, jobID, profile)
	view.HasSaved = saved != nil && strings.TrimSpace(saved.Markdown) != ""

	base := strings.TrimSpace(baseOverride)
	if base == "" && view.HasSaved {
		base = saved.Markdown
	}
	if base == "" && templateID != resume.DefaultTemplateID {
		if tmd, _ := h.Templates.GetMarkdown(ctx, profile, templateID); strings.TrimSpace(tmd) != "" {
			base = tmd
		}
	}
	if base == "" {
		base = canonicalMD
	}
	view.BaseMarkdown = base

	view.TemplateName = "Full résumé"
	if list, err := h.Templates.List(ctx, profile); err == nil {
		for _, t := range list {
			view.ReplaceTargets = append(view.ReplaceTargets, templateOpt{ID: t.ID, Name: t.Name, IsDefault: t.IsDefault})
			if t.ID == templateID {
				view.TemplateName = t.Name
			}
		}
	}
	return view, nil
}

// DraftFragment handles GET /ui/jobs/{id}/draft?profile=… — renders the two-pane
// markdown view, with the right pane showing the latest AI diff if one exists.
func (h *ResumeHandler) DraftFragment(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))
	templateID := templateOrDefault(r.URL.Query().Get("template"))

	payload, draftedAt, err := h.latestDraftEvent(r.Context(), jobID, profile)
	if err != nil {
		http.Error(w, "load draft: "+err.Error(), http.StatusInternalServerError)
		return
	}
	view, herr := h.markdownFragment(r.Context(), jobID, profile, templateID, "", "", payload, draftedAt)
	if herr != nil {
		http.Error(w, herr.msg, herr.status)
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "draft_fragment", view)
}

// DraftFragmentTrigger handles POST /ui/jobs/{id}/draft — runs a fresh DeepSeek
// draft, persists the event, and re-renders the fragment with the diff. The
// left-pane markdown (carried in the form) is preserved across a re-draft.
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
	view, herr := h.markdownFragment(r.Context(), jobID, profile, templateID, r.FormValue("markdown"), "", payload, "just now")
	if herr != nil {
		http.Error(w, herr.msg, herr.status)
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "draft_fragment", view)
}

// ApplyAI handles POST /ui/jobs/{id}/apply-ai — accept the right pane into the
// left: re-render with the working copy replaced by the (possibly hand-edited)
// tailored markdown the right pane posts back. The right pane keeps that same
// edited text so nothing is lost, and the diff recomputes against it.
func (h *ResumeHandler) ApplyAI(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))
	templateID := templateOrDefault(r.FormValue("template"))

	payload, draftedAt, err := h.latestDraftEvent(r.Context(), jobID, profile)
	if err != nil {
		http.Error(w, "load draft: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if payload == nil {
		http.Error(w, "no draft to apply — click Draft with AI first", http.StatusBadRequest)
		return
	}
	// The edited right pane becomes both the new working copy (left) and the
	// right pane's content, so both panes and their diffs reflect it.
	tailored := r.FormValue("tailored")
	view, herr := h.markdownFragment(r.Context(), jobID, profile, templateID, tailored, tailored, payload, draftedAt)
	if herr != nil {
		http.Error(w, herr.msg, herr.status)
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "draft_fragment", view)
}

// ── internals ───────────────────────────────────────────────────────────────

// draftedEventPayload is the JSON shape we store in application_events.payload
// for resume_drafted, mirrored here so both the writer and the fragment reader
// agree on field names.
type draftedEventPayload struct {
	PromptVersion     string                      `json:"prompt_version"`
	Model             string                      `json:"model"`
	ResumeVersion     string                      `json:"resume_version"`
	CostUSD           float64                     `json:"cost_usd"`
	BulletCount       int                         `json:"bullet_count"`
	Removals          []deepseek.Removal          `json:"removals"`
	Rewrites          []deepseek.Rewrite          `json:"rewrites"`
	EducationRemovals []deepseek.EducationRemoval `json:"education_removals"`
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

// buildRoleScores folds the flat bullet pool (canonical order) plus the flat
// score list into per-role scored bullets for resumesuggest.Select.
func buildRoleScores(bullets []resume.Bullet, scores []deepseek.BulletScore) []resumesuggest.RoleScores {
	scoreOf := make(map[string]int, len(scores))
	for _, s := range scores {
		scoreOf[s.RoleID+"."+s.BulletID] = s.Score
	}
	var roles []resumesuggest.RoleScores
	idx := map[string]int{}
	for _, b := range bullets {
		i, ok := idx[b.RoleID]
		if !ok {
			i = len(roles)
			idx[b.RoleID] = i
			roles = append(roles, resumesuggest.RoleScores{RoleID: b.RoleID})
		}
		roles[i].Bullets = append(roles[i].Bullets, resumesuggest.ScoredBullet{BulletID: b.BulletID, Score: scoreOf[b.CompositeID()]})
	}
	return roles
}

// scoreRemovals turns the selection's cut set into removal records, in canonical
// order, each carrying the score that lost it its slot.
func scoreRemovals(bullets []resume.Bullet, sel resumesuggest.Selection) []deepseek.Removal {
	var out []deepseek.Removal
	for _, b := range bullets {
		id := b.CompositeID()
		if sel.IsKept(id) {
			continue
		}
		out = append(out, deepseek.Removal{
			RoleID:   b.RoleID,
			BulletID: b.BulletID,
			Reason:   fmt.Sprintf("relevance %d — below this role's keep cut for the posting", sel.ScoreOf[id]),
		})
	}
	return out
}

// keptRewrites drops any rewrite that targets a cut bullet — only rewrites on
// kept bullets reach the résumé.
func keptRewrites(rewrites []deepseek.Rewrite, sel resumesuggest.Selection) []deepseek.Rewrite {
	var out []deepseek.Rewrite
	for _, rw := range rewrites {
		if sel.IsKept(rw.RoleID + "." + rw.BulletID) {
			out = append(out, rw)
		}
	}
	return out
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

	// The full document gives the model the supplementary education entries it
	// may prune (exams/certs); education is profile-wide, not template-specific.
	doc, err := resume.LoadDocument(ctx, h.Pool, profile)
	if err != nil {
		return nil, nil, nil, &handlerErr{http.StatusInternalServerError, "load resume: " + err.Error()}
	}

	draft, err := h.DeepSeek.Draft(ctx, *job.Description, res.Bullets, doc.Education)
	if err != nil {
		return nil, nil, nil, &handlerErr{http.StatusBadGateway, "deepseek: " + err.Error()}
	}

	// v10: relevance scoring governs removals. The calibrated flash scorer
	// (deepseek.ScoreBullets, "A_clean" prompt) ranks every bullet against the
	// posting; the clamp(count≥threshold, floor, cap) rule in resumesuggest
	// picks the keep set, replacing the tailoring prompt's deliberately
	// conservative removals. Rewrites are filtered to the kept set — rewriting a
	// bullet we're about to cut is wasted. Best-effort: a scorer failure leaves
	// the prompt's own removals/rewrites in place so a draft still works.
	jobText := *job.Description
	if job.Title != nil && *job.Title != "" {
		jobText = *job.Title + "\n" + *job.Description
	}
	var scores []deepseek.BulletScore
	if sc, serr := h.DeepSeek.ScoreBullets(ctx, jobText, res.Bullets); serr == nil && len(sc) > 0 {
		scores = sc
		sel := resumesuggest.Select(buildRoleScores(res.Bullets, sc), resumesuggest.DefaultLimits, resumesuggest.DefaultThreshold, resumesuggest.DefaultImportantThreshold)
		draft.Removals = scoreRemovals(res.Bullets, sel)
		draft.Rewrites = keptRewrites(draft.Rewrites, sel)
	}

	payload, _ := json.Marshal(map[string]any{
		"prompt_version":      draft.PromptVersion,
		"scorer_version":      deepseek.ScorerVersion,
		"relevance_threshold": resumesuggest.DefaultThreshold,
		"important_threshold": resumesuggest.DefaultImportantThreshold,
		"model":               draft.Model,
		"resume_version":      res.Version,
		"prompt_tokens":       draft.Usage.PromptTokens,
		"completion_tokens":   draft.Usage.CompletionTokens,
		"total_tokens":        draft.Usage.TotalTokens,
		"cost_usd":            draft.Usage.CostUSD,
		"bullet_count":        len(res.Bullets),
		"removals":            draft.Removals,
		"rewrites":            draft.Rewrites,
		"education_removals":  draft.EducationRemovals,
		"scores":              scores,
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
		PromptVersion:     draft.PromptVersion,
		Model:             draft.Model,
		ResumeVersion:     res.Version,
		CostUSD:           draft.Usage.CostUSD,
		BulletCount:       len(res.Bullets),
		Removals:          draft.Removals,
		Rewrites:          draft.Rewrites,
		EducationRemovals: draft.EducationRemovals,
	}
}

// draftEventPayload (above) plus markdownFragment (above) cover the rendering
// path; the old per-bullet checkbox view builders were removed with the move to
// the markdown two-pane workflow.
