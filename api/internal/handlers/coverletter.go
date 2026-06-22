package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

// coverLetterView feeds web/templates/cover_letter.html — the editable letter
// pane in the job workspace's collapsible cover-letter section.
type coverLetterView struct {
	JobID     string
	Profile   string
	Body      string
	Model     string // LLM that produced the last AI draft, "" if hand-written
	UpdatedAt string
	HasLetter bool   // a letter exists (saved or just drafted) → show the editor
	Note      string // transient status line after a save or AI draft
}

// CoverLetterFragment handles GET /ui/jobs/{id}/cover-letter — the saved
// letter if one exists, else the empty state with the AI-draft trigger.
func (h *ResumeHandler) CoverLetterFragment(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))

	view := coverLetterView{JobID: jobID, Profile: profile}
	cl, err := h.CoverLetters.Get(r.Context(), jobID, profile)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load cover letter: "+err.Error())
		return
	}
	if cl != nil {
		view.Body = cl.Body
		view.Model = cl.Model
		view.UpdatedAt = cl.UpdatedAt
		view.HasLetter = true
	}
	h.Renderer.HTML(w, http.StatusOK, "cover_letter", view)
}

// SaveCoverLetter handles POST /ui/jobs/{id}/cover-letter — persist the
// (possibly hand-edited) letter body from the textarea.
func (h *ResumeHandler) SaveCoverLetter(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "bad form")
		return
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))
	body := r.FormValue("body")
	if strings.TrimSpace(body) == "" {
		writeErr(w, http.StatusBadRequest, "empty cover letter")
		return
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

	// model="" keeps the stored provenance — editing an AI draft doesn't
	// erase which model wrote it.
	saved, err := h.CoverLetters.Save(r.Context(), jobID, profile, body, "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "cover_letter", coverLetterView{
		JobID:     jobID,
		Profile:   profile,
		Body:      saved.Body,
		Model:     saved.Model,
		UpdatedAt: saved.UpdatedAt,
		HasLetter: true,
		Note:      "Saved ✓",
	})
}

// CoverLetterPDF handles POST /ui/jobs/{id}/cover-letter.pdf — the "PDF"
// button. It saves the textarea body (so the file matches what's stored) then
// streams a rendered PDF from resume_htmx as a download. Native form submit
// with formtarget=_blank, mirroring the résumé "Generate PDF" button — and a
// download (not inline) so it lands on disk even when the browser's in-tab PDF
// viewer is misbehaving.
func (h *ResumeHandler) CoverLetterPDF(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "bad form")
		return
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))
	body := r.FormValue("body")
	if strings.TrimSpace(body) == "" {
		writeErr(w, http.StatusBadRequest, "empty cover letter")
		return
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
	if _, err := h.CoverLetters.Save(r.Context(), jobID, profile, body, ""); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Letterhead pulled from the résumé contact so the letter matches the
	// résumé's header; rendered via resume_htmx's cover-letter endpoint, which
	// preserves line breaks (nl2br) the markdown résumé path collapses.
	name, contact := h.letterhead(r.Context(), profile)
	fn := pdfFilename(name, "coverletter", h.jobTitle(r.Context(), jobID))
	h.proxyPDF(w, r, "/render-cover-letter-pdf", map[string]any{
		"body":    body,
		"name":    name,
		"contact": contact,
		"date":    time.Now().Format("January 2, 2006"),
	}, fn, "attachment")
}

// letterhead returns the candidate's name and a "·"-joined contact line from
// the canonical résumé, matching the résumé PDF's header. Best-effort: empty
// strings if the résumé can't be loaded (the template just omits them).
func (h *ResumeHandler) letterhead(ctx context.Context, profile string) (name, contact string) {
	doc, err := resume.LoadDocument(ctx, h.Pool, profile)
	if err != nil || doc == nil {
		return "", ""
	}
	parts := make([]string, 0, 4)
	for _, s := range []string{doc.Contact.Email, doc.Contact.Phone, doc.Contact.Github, doc.Contact.Location} {
		if strings.TrimSpace(s) != "" {
			parts = append(parts, s)
		}
	}
	return doc.Contact.Name, strings.Join(parts, " · ")
}

// DraftCoverLetter handles POST /ui/jobs/{id}/cover-letter/draft — calls
// DeepSeek with the job posting and the candidate's résumé and renders the
// generated letter into the editor. The draft is NOT auto-saved: the user
// reviews, edits, then clicks Save.
func (h *ResumeHandler) DraftCoverLetter(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "bad form")
		return
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))

	if h.DeepSeek == nil {
		writeErr(w, http.StatusServiceUnavailable, "DeepSeek not configured (set DEEPSEEK_API_KEY)")
		return
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
		writeErr(w, http.StatusBadRequest, "job has no description to write against")
		return
	}

	resumeMD, herr := h.resumeMarkdownFor(r.Context(), jobID, profile)
	if herr != nil {
		writeErr(w, herr.status, herr.msg)
		return
	}

	title, company := "", ""
	if job.Title != nil {
		title = *job.Title
	}
	if job.Company != nil {
		company = *job.Company
	}
	result, err := h.DeepSeek.CoverLetter(r.Context(), title, company, *job.Description, resumeMD)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "deepseek: "+err.Error())
		return
	}

	// Audit trail with cost telemetry, same shape as resume_drafted.
	_ = db.WriteEvent(r.Context(), h.Pool, profile, jobID, "cover_letter_drafted", map[string]any{
		"prompt_version":    result.PromptVersion,
		"model":             result.Model,
		"prompt_tokens":     result.Usage.PromptTokens,
		"completion_tokens": result.Usage.CompletionTokens,
		"total_tokens":      result.Usage.TotalTokens,
		"cost_usd":          result.Usage.CostUSD,
	})

	h.Renderer.HTML(w, http.StatusOK, "cover_letter", coverLetterView{
		JobID:     jobID,
		Profile:   profile,
		Body:      result.Text,
		Model:     result.Model,
		HasLetter: true,
		Note:      "AI draft — review, edit, then Save",
	})
}

// resumeMarkdownFor returns the résumé the cover letter should be grounded
// in: the saved tailored résumé for this job when one exists, else the
// profile's default-template render.
func (h *ResumeHandler) resumeMarkdownFor(ctx context.Context, jobID, profile string) (string, *handlerErr) {
	if fin, _ := h.Finalizations.Get(ctx, jobID, profile); fin != nil && strings.TrimSpace(fin.Markdown) != "" {
		return fin.Markdown, nil
	}
	res, err := resume.LoadTemplate(ctx, h.Pool, profile, resume.DefaultTemplateID)
	if err != nil {
		return "", &handlerErr{http.StatusInternalServerError, "resume load: " + err.Error()}
	}
	doc, err := resume.LoadDocument(ctx, h.Pool, profile)
	if err != nil {
		return "", &handlerErr{http.StatusInternalServerError, "load resume: " + err.Error()}
	}
	return resume.ToMarkdown(doc, res.Bullets), nil
}
