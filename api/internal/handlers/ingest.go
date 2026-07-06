package handlers

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resumeingest"
)

// maxIngestBytes bounds the pasted résumé — far above any real résumé, low
// enough to keep the LLM call sane.
const maxIngestBytes = 100 << 10

// IngestHandler serves /resume/ingest: paste a résumé in any textual form and
// DeepSeek structures it (extraction only — no invented content) into BOTH
// stores: web.resume_master markdown and the structured web.resume_* rows the
// tailoring flow reads. Replacing an existing résumé requires the explicit
// confirm checkbox; the swap is transactional.
type IngestHandler struct {
	DeepSeek *deepseek.Client // nil when DEEPSEEK_API_KEY is unset
	Pool     *pgxpool.Pool
	Renderer *render.Renderer
}

type ingestView struct {
	Profile     string
	Text        string // pasted content, retained on error
	HasExisting bool
	Error       string
	Done        bool
	Counts      resumeingest.Counts
}

// Page handles GET /resume/ingest.
func (h *IngestHandler) Page(w http.ResponseWriter, r *http.Request) {
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))
	v := ingestView{Profile: profile}
	if has, err := resumeingest.HasExisting(r.Context(), h.Pool, profile); err == nil {
		v.HasExisting = has
	}
	h.Renderer.HTML(w, http.StatusOK, "ingest", v)
}

// Ingest handles POST /resume/ingest.
func (h *IngestHandler) Ingest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxIngestBytes+4096)
	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "form too large or malformed")
		return
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))
	text := strings.TrimSpace(r.FormValue("resume"))
	replace := r.FormValue("replace") == "on"

	v := ingestView{Profile: profile, Text: text}
	if has, err := resumeingest.HasExisting(r.Context(), h.Pool, profile); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	} else {
		v.HasExisting = has
	}

	fail := func(msg string) {
		v.Error = msg
		h.Renderer.HTML(w, http.StatusOK, "ingest", v)
	}

	switch {
	case h.DeepSeek == nil:
		fail("résumé parsing is unavailable: DEEPSEEK_API_KEY is not configured")
		return
	case len(text) < 200:
		fail("that looks too short to be a résumé — paste the full text")
		return
	case len(text) > maxIngestBytes:
		fail("résumé text is over 100KB — paste text, not a binary export")
		return
	case v.HasExisting && !replace:
		fail("this profile already has a résumé — tick the replace box to overwrite it")
		return
	}

	raw, _, err := h.DeepSeek.StructureResume(r.Context(), text)
	if err != nil {
		fail("parsing failed: " + err.Error())
		return
	}
	s, err := resumeingest.Parse(raw)
	if err != nil {
		fail(err.Error())
		return
	}
	counts, err := resumeingest.Replace(r.Context(), h.Pool, profile, text, s)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	v.Done, v.Counts, v.Text, v.HasExisting = true, counts, "", true
	h.Renderer.HTML(w, http.StatusOK, "ingest", v)
}
