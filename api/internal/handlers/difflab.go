package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

// diffLabView feeds web/templates/difflab.html — a standalone two-pane résumé
// diff sandbox served at /v1, /v2, /v3. The three variants are identical except
// for how they highlight differences (overlay / CodeMirror / on-demand compare),
// so they can be compared side by side before one is chosen.
type diffLabView struct {
	Variant        string // "v1" | "v2" | "v3"
	JobID          string
	Profile        string
	JobTitle       string
	MasterMarkdown string // left pane — the permanent master résumé
	JobMarkdown    string // right pane — the version tailored for this job
}

// DiffLab renders the diff-lab page for a given highlighting variant. ?job=<id>
// chooses the job for the right pane; absent, it falls back to the most recently
// AI-drafted job so there's something to diff against.
func (h *ResumeHandler) DiffLab(w http.ResponseWriter, r *http.Request, variant string) {
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))
	jobID := r.URL.Query().Get("job")
	if jobID == "" {
		jobID = h.latestDraftedJob(r.Context(), profile)
	}

	master := h.masterMarkdown(r.Context(), profile)
	view := diffLabView{
		Variant:        variant,
		JobID:          jobID,
		Profile:        profile,
		JobTitle:       h.jobTitle(r.Context(), jobID),
		MasterMarkdown: master,
		JobMarkdown:    h.jobMarkdown(r.Context(), jobID, profile, master),
	}
	h.Renderer.HTML(w, http.StatusOK, "difflab", view)
}

// SaveMaster handles POST /ui/resume/master — persist the left pane's markdown
// as the profile's permanent master résumé. Returns a tiny text status the
// page surfaces as a toast.
func (h *ResumeHandler) SaveMaster(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))
	md := r.FormValue("markdown")
	if strings.TrimSpace(md) == "" {
		http.Error(w, "empty résumé markdown", http.StatusBadRequest)
		return
	}
	if h.Master == nil {
		http.Error(w, "master résumé store not configured", http.StatusServiceUnavailable)
		return
	}
	if err := h.Master.Save(r.Context(), profile, md); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("master saved"))
}

// masterMarkdown returns the stored master résumé, or the canonical structured
// résumé rendered to markdown when none has been saved yet.
func (h *ResumeHandler) masterMarkdown(ctx context.Context, profile string) string {
	if h.Master != nil {
		if m, _ := h.Master.Get(ctx, profile); strings.TrimSpace(m) != "" {
			return m
		}
	}
	res, err := resume.LoadTemplate(ctx, h.Pool, profile, resume.DefaultTemplateID)
	if err != nil {
		return ""
	}
	doc, _ := resume.LoadDocument(ctx, h.Pool, profile)
	return resume.ToMarkdown(doc, res.Bullets)
}

// jobMarkdown returns the right-pane content for a job: the saved tailored
// résumé if one exists, else the latest AI-tailored draft rendered to markdown,
// else the master copy (so the panes simply match when there's nothing to diff).
func (h *ResumeHandler) jobMarkdown(ctx context.Context, jobID, profile, masterFallback string) string {
	if jobID != "" {
		if fin, _ := h.Finalizations.Get(ctx, jobID, profile); fin != nil && strings.TrimSpace(fin.Markdown) != "" {
			return fin.Markdown
		}
		if md, ok := h.tailoredMarkdownForJob(ctx, jobID, profile); ok {
			return md
		}
	}
	return masterFallback
}

// tailoredMarkdownForJob renders the latest AI draft's removals+rewrites against
// the full résumé into markdown. Returns ("", false) when there's no draft.
func (h *ResumeHandler) tailoredMarkdownForJob(ctx context.Context, jobID, profile string) (string, bool) {
	payload, _, err := h.latestDraftEvent(ctx, jobID, profile)
	if err != nil || payload == nil {
		return "", false
	}
	res, err := resume.LoadTemplate(ctx, h.Pool, profile, resume.DefaultTemplateID)
	if err != nil {
		return "", false
	}
	doc, err := resume.LoadDocument(ctx, h.Pool, profile)
	if err != nil {
		return "", false
	}
	removed := map[string]string{}
	for _, rm := range payload.effectiveRemovals() {
		removed[rm.RoleID+"."+rm.BulletID] = rm.Reason
	}
	rewritten := map[string]deepseek.Rewrite{}
	for _, rw := range payload.Rewrites {
		rewritten[rw.RoleID+"."+rw.BulletID] = rw
	}
	return resume.ToMarkdown(doc, tailoredBullets(res.Bullets, removed, rewritten)), true
}

// latestDraftedJob returns the job_id of the most recent AI draft for a profile,
// or "" if there are none — used as the diff-lab's default right-pane job.
func (h *ResumeHandler) latestDraftedJob(ctx context.Context, profile string) string {
	var jobID string
	err := h.Pool.QueryRow(ctx, `
        SELECT job_id FROM web.application_events
        WHERE event_type = 'resume_drafted' AND sys_profile = $1
        ORDER BY created_at DESC LIMIT 1
    `, profile).Scan(&jobID)
	if err != nil && err != pgx.ErrNoRows {
		return ""
	}
	return jobID
}
