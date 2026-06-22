package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
)

var pdfHTTPClient = &http.Client{Timeout: 30 * time.Second}

// resumeHTMXURL is the sibling Flask app that owns PDF rendering (WeasyPrint).
func resumeHTMXURL() string {
	if u := os.Getenv("RESUME_HTMX_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost:5000"
}

// ResumePDF handles GET /ui/jobs/{id}/resume.pdf?profile=… — it reads the
// finalized resume markdown saved by Generate and POSTs it to resume_htmx's
// /render-markdown-pdf, which converts markdown → HTML → PDF (WeasyPrint). We
// own the data; resume_htmx owns the rendering. ?format=html previews the
// styled HTML without WeasyPrint.
func (h *ResumeHandler) ResumePDF(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))

	saved, err := h.Finalizations.Get(r.Context(), jobID, profile)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if saved == nil || strings.TrimSpace(saved.Markdown) == "" {
		writeErr(w, http.StatusNotFound, "no saved resume for this job — click Save résumé first")
		return
	}
	name := nameFromMarkdown(saved.Markdown)
	fn := pdfFilename(name, "resume", h.jobTitle(r.Context(), jobID))
	h.streamPDF(w, r, saved.Markdown, name, fn, "inline")
}

// GeneratePDF handles POST /ui/jobs/{id}/resume.pdf — the "Generate PDF"
// button. It persists the working-copy markdown exactly like GenerateResume
// (so the PDF always matches what was saved), then streams the rendered PDF.
// The button is a native form submit with formtarget=_blank, so the PDF opens
// in a new tab while the workspace stays put.
func (h *ResumeHandler) GeneratePDF(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	saved, _, _, herr := h.saveResumeFromForm(r, jobID)
	if herr != nil {
		writeErr(w, herr.status, herr.msg)
		return
	}
	name := nameFromMarkdown(saved.Markdown)
	fn := pdfFilename(name, "resume", h.jobTitle(r.Context(), jobID))
	h.streamPDF(w, r, saved.Markdown, name, fn, "inline")
}

// streamPDF proxies résumé markdown to resume_htmx's /render-markdown-pdf. name
// is the document name resume_htmx uses for its title; filename/disposition set
// the download headers ("inline" to view in-tab, "attachment" to save a file).
func (h *ResumeHandler) streamPDF(w http.ResponseWriter, r *http.Request, markdown, name, filename, disposition string) {
	h.proxyPDF(w, r, "/render-markdown-pdf", map[string]any{
		"markdown": markdown,
		"name":     name,
	}, filename, disposition)
}

// proxyPDF POSTs payload to a resume_htmx render endpoint and copies the
// response (PDF, or styled HTML with ?format=html) to the client with the
// given download headers.
func (h *ResumeHandler) proxyPDF(w http.ResponseWriter, r *http.Request, path string, payload any, filename, disposition string) {
	body, _ := json.Marshal(payload)

	url := resumeHTMXURL() + path
	if r.URL.Query().Get("format") == "html" {
		url += "?format=html"
	}
	req, err := http.NewRequestWithContext(r.Context(), "POST", url, bytes.NewReader(body))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := pdfHTTPClient.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "resume_htmx unreachable at "+resumeHTMXURL()+": "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("resume_htmx render failed (%d): %s", resp.StatusCode, msg))
		return
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/pdf"
	}
	w.Header().Set("Content-Type", ct)
	if disposition == "" {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, filename))
	_, _ = io.Copy(w, resp.Body)
}

// nameFromMarkdown pulls the resume owner's name from the leading "# Name"
// heading, used only for the PDF filename. Falls back to "resume".
func nameFromMarkdown(md string) string {
	for _, ln := range strings.Split(md, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "# ") {
			return strings.TrimSpace(ln[2:])
		}
		if ln != "" {
			break
		}
	}
	return "resume"
}

// jobTitle returns the job's posting title for use in a download filename, or
// "" if the job can't be loaded or has no title. Best-effort.
func (h *ResumeHandler) jobTitle(ctx context.Context, jobID string) string {
	if j, err := h.Jobs.Get(ctx, jobID); err == nil && j != nil && j.Title != nil {
		return *j.Title
	}
	return ""
}

// pdfFilename builds a descriptive download name like
// "slava-gorelik-resume-data-engineer-security.pdf" from the candidate name,
// document type ("resume"/"coverletter"), and job title. Missing or redundant
// pieces are dropped, so it degrades to e.g. "resume.pdf".
func pdfFilename(name, docType, jobTitle string) string {
	parts := make([]string, 0, 3)
	if s := slugify(name); s != "" && s != docType {
		parts = append(parts, s)
	}
	parts = append(parts, docType)
	if s := slugify(jobTitle); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, "-") + ".pdf"
}

// slugify lowercases s and collapses every run of non-alphanumeric characters
// into a single hyphen, trimming leading/trailing hyphens. ASCII-only — good
// enough for names and job titles in download filenames.
func slugify(s string) string {
	var b strings.Builder
	pendingHyphen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			if pendingHyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingHyphen = false
			b.WriteRune(r)
		default:
			pendingHyphen = true
		}
	}
	return b.String()
}
