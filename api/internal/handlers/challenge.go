package handlers

import (
	"archive/zip"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/challenges"
	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
)

// challengeView feeds web/templates/challenge.html — the practice-exercise
// pane in the job workspace's collapsible "Practice" section.
type challengeView struct {
	JobID    string
	Profile  string
	Has      bool // an exercise exists → show it instead of the generate trigger
	Title    string
	Brief    string
	Skills   []string
	Minutes  int
	Files    []deepseek.ChallengeFile
	Model    string
	Updated  string
	Solution []deepseek.ChallengeFile // populated only on the reveal request
	Note     string
}

// ChallengeFragment handles GET /ui/jobs/{id}/challenge — the saved exercise
// if one exists, else the empty state with the generate trigger.
func (h *ResumeHandler) ChallengeFragment(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))

	view := challengeView{JobID: jobID, Profile: profile}
	ch, err := h.Challenges.Get(r.Context(), jobID, profile)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load challenge: "+err.Error())
		return
	}
	if ch != nil {
		fillChallengeView(&view, ch)
	}
	h.Renderer.HTML(w, http.StatusOK, "challenge", view)
}

// DraftChallenge handles POST /ui/jobs/{id}/challenge/draft — generates a
// practice exercise from the posting and saves it. Unlike a cover letter
// there's no hand-edited state to protect, so the draft is persisted straight
// away: the value is having it on disk when you sit down, not editing it.
func (h *ResumeHandler) DraftChallenge(w http.ResponseWriter, r *http.Request) {
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
		writeErr(w, http.StatusBadRequest, "job has no description to build an exercise from")
		return
	}

	title, company := "", ""
	if job.Title != nil {
		title = *job.Title
	}
	if job.Company != nil {
		company = *job.Company
	}
	result, err := h.DeepSeek.Challenge(r.Context(), title, company, *job.Description)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "deepseek: "+err.Error())
		return
	}

	// Audit trail with cost telemetry, same shape as cover_letter_drafted.
	_ = db.WriteEvent(r.Context(), h.Pool, profile, jobID, "challenge_drafted", map[string]any{
		"prompt_version":    result.PromptVersion,
		"model":             result.Model,
		"title":             result.Title,
		"minutes":           result.Minutes,
		"skills":            result.Skills,
		"prompt_tokens":     result.Usage.PromptTokens,
		"completion_tokens": result.Usage.CompletionTokens,
		"total_tokens":      result.Usage.TotalTokens,
		"cost_usd":          result.Usage.CostUSD,
	})

	saved, err := h.Challenges.Save(r.Context(), jobID, profile, &challenges.Challenge{
		Title:         result.Title,
		Brief:         result.Brief,
		Skills:        result.Skills,
		Minutes:       result.Minutes,
		Files:         result.Files,
		Solution:      result.Solution,
		Model:         result.Model,
		PromptVersion: result.PromptVersion,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	view := challengeView{JobID: jobID, Profile: profile, Note: "Generated — download and run pytest"}
	fillChallengeView(&view, saved)
	h.Renderer.HTML(w, http.StatusOK, "challenge", view)
}

// ChallengeSolution handles POST /ui/jobs/{id}/challenge/solution — re-renders
// the pane with the reference implementation shown. Deliberately a separate
// request so the answer never ships with the exercise.
func (h *ResumeHandler) ChallengeSolution(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "bad form")
		return
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))

	ch, err := h.Challenges.Get(r.Context(), jobID, profile)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load challenge: "+err.Error())
		return
	}
	if ch == nil {
		writeErr(w, http.StatusNotFound, "no challenge for this job")
		return
	}
	view := challengeView{JobID: jobID, Profile: profile}
	fillChallengeView(&view, ch)
	view.Solution = ch.Solution
	if len(view.Solution) == 0 {
		view.Note = "no reference solution was generated for this exercise"
	}
	h.Renderer.HTML(w, http.StatusOK, "challenge", view)
}

// ChallengeZip handles GET /ui/jobs/{id}/challenge.zip — the exercise files as
// a download, ready to unzip and run pytest in. The reference solution is NOT
// included; that's what the reveal button is for.
func (h *ResumeHandler) ChallengeZip(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))

	ch, err := h.Challenges.Get(r.Context(), jobID, profile)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load challenge: "+err.Error())
		return
	}
	if ch == nil {
		writeErr(w, http.StatusNotFound, "no challenge for this job")
		return
	}

	root := challengeSlug(ch.Title)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", root+".zip"))

	zw := zip.NewWriter(w)
	defer zw.Close()

	// README first so an unzip lands the brief at the top of the listing.
	if err := writeZipEntry(zw, root+"/README.md", challengeReadme(ch)); err != nil {
		return // headers already sent; nothing useful to report to the client
	}
	for _, f := range ch.Files {
		// Paths were validated at generation time; re-check here because the
		// row may predate that validation.
		if err := deepseek.ValidateChallengePath(f.Path); err != nil {
			continue
		}
		if err := writeZipEntry(zw, root+"/"+f.Path, f.Content); err != nil {
			return
		}
	}
}

func writeZipEntry(zw *zip.Writer, name, content string) error {
	fw, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = fw.Write([]byte(content))
	return err
}

// challengeReadme is the brief plus the house rule that makes this drill
// worth doing: read the failing test before touching any code.
func challengeReadme(ch *challenges.Challenge) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", ch.Title)
	if ch.Minutes > 0 {
		fmt.Fprintf(&b, "**Time budget:** %d minutes\n\n", ch.Minutes)
	}
	if len(ch.Skills) > 0 {
		fmt.Fprintf(&b, "**Drills:** %s\n\n", strings.Join(ch.Skills, ", "))
	}
	b.WriteString(strings.TrimSpace(ch.Brief))
	b.WriteString("\n\n---\n\n## How to run\n\n```\npytest\n```\n\n")
	b.WriteString("## The rule\n\n")
	b.WriteString("One module already contains a deliberate bug, and you are not told which.\n")
	b.WriteString("Before editing anything, run the suite and read the failing test — its name\n")
	b.WriteString("and its assertion tell you which unit owns the failure. Name the module out\n")
	b.WriteString("loud, then start typing. Fixing the wrong file costs the clock twice.\n")
	return b.String()
}

func fillChallengeView(v *challengeView, ch *challenges.Challenge) {
	v.Has = true
	v.Title = ch.Title
	v.Brief = ch.Brief
	v.Skills = ch.Skills
	v.Minutes = ch.Minutes
	v.Files = ch.Files
	v.Model = ch.Model
	v.Updated = ch.UpdatedAt
}

var challengeSlugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// challengeSlug turns an exercise title into a safe directory name.
func challengeSlug(title string) string {
	s := challengeSlugStrip.ReplaceAllString(strings.ToLower(strings.TrimSpace(title)), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "practice-challenge"
	}
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	return s
}
