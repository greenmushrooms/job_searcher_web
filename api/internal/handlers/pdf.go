package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

var pdfHTTPClient = &http.Client{Timeout: 30 * time.Second}

// resumeHTMXURL is the sibling Flask app that owns PDF rendering (WeasyPrint).
func resumeHTMXURL() string {
	if u := os.Getenv("RESUME_HTMX_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost:5000"
}

// ResumePDF handles GET /ui/jobs/{id}/resume.pdf?profile=… — it builds the
// tailored document from the DB (the full resume + the kept-bullet selection
// stored in jobs_resume) and POSTs it to resume_htmx, which renders the PDF.
// We own the data; resume_htmx owns the rendering. ?format=html is passed
// through for an HTML preview that doesn't need WeasyPrint.
func (h *ResumeHandler) ResumePDF(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))

	saved, err := h.Finalizations.Get(r.Context(), jobID, profile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if saved == nil {
		http.Error(w, "no generated resume for this job — click Generate first", http.StatusNotFound)
		return
	}

	doc, err := resume.LoadDocument(r.Context(), h.Pool, profile)
	if err != nil {
		http.Error(w, "load resume: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Prefer the stored per-bullet snapshot (carries manual edits + accepted AI
	// rewrites); fall back to the kept-id selection for legacy rows.
	selections := groupByRole(saved.KeptBulletIDs)
	if final := parseFinalBullets(saved.Bullets); len(final) > 0 {
		selections = applyFinalBullets(doc, final)
	}

	body, _ := json.Marshal(map[string]any{
		"resume":     doc,
		"selections": selections,
	})

	url := resumeHTMXURL() + "/render-pdf"
	if r.URL.Query().Get("format") == "html" {
		url += "?format=html"
	}
	req, err := http.NewRequestWithContext(r.Context(), "POST", url, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := pdfHTTPClient.Do(req)
	if err != nil {
		http.Error(w, "resume_htmx unreachable at "+resumeHTMXURL()+": "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		http.Error(w, fmt.Sprintf("resume_htmx render failed (%d): %s", resp.StatusCode, msg), http.StatusBadGateway)
		return
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/pdf"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", `inline; filename="tailored_resume.pdf"`)
	_, _ = io.Copy(w, resp.Body)
}

// savedBullet mirrors finalizations' stored bullet snapshot
// ([{role_id,bullet_id,text,source}]).
type savedBullet struct {
	RoleID   string `json:"role_id"`
	BulletID string `json:"bullet_id"`
	Text     string `json:"text"`
	Source   string `json:"source"`
}

func parseFinalBullets(raw []byte) []savedBullet {
	if len(raw) == 0 {
		return nil
	}
	var out []savedBullet
	_ = json.Unmarshal(raw, &out)
	return out
}

// applyFinalBullets overlays the stored final text onto the canonical document
// and returns the selection (role -> bullet ids) in stored order. A bullet
// missing from canonical (e.g. since retired) is re-added with its stored text
// so the generated resume still renders what the user saw.
func applyFinalBullets(doc *resume.Document, final []savedBullet) map[string][]string {
	roleIdx := make(map[string]int, len(doc.Experience))
	for i := range doc.Experience {
		roleIdx[doc.Experience[i].ID] = i
	}
	sel := map[string][]string{}
	for _, fb := range final {
		sel[fb.RoleID] = append(sel[fb.RoleID], fb.BulletID)
		idx, ok := roleIdx[fb.RoleID]
		if !ok {
			continue
		}
		b := doc.Experience[idx].Bullets[fb.BulletID]
		b.Text = fb.Text
		doc.Experience[idx].Bullets[fb.BulletID] = b
	}
	return sel
}

// groupByRole turns ["role.bullet", …] into {role: [bullet, …]} preserving
// order — the selections shape resume_htmx's template expects. Splits on the
// first dot (role ids carry no dots; CompositeID is role+"."+bullet).
func groupByRole(compositeIDs []string) map[string][]string {
	out := map[string][]string{}
	for _, cid := range compositeIDs {
		if dot := strings.IndexByte(cid, '.'); dot >= 0 {
			role, bullet := cid[:dot], cid[dot+1:]
			out[role] = append(out[role], bullet)
		}
	}
	return out
}
