package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

// The left-hand canonical resume editor. All routes are htmx fragment swaps,
// no JS: editing/adding/retiring a bullet writes to web.resume_bullets and
// swaps the affected markup back. The right-hand per-job draft is unaffected —
// it reads the same pool on its next load.

type resumeEditorView struct {
	Profile string
	Roles   []resumeRoleEditView
}

type resumeRoleEditView struct {
	RoleID  string
	Title   string
	Company string
	Dates   string
	Bullets []resumeBulletEditView
}

// resumeBulletEditView carries everything resume_bullet.html needs to render
// standalone after an edit/add swap — including Profile, so the row's own
// hx-vals can post back without the parent form.
type resumeBulletEditView struct {
	Profile  string
	RoleID   string
	BulletID string
	Text     string
}

// ResumeEditor handles GET /ui/resume?profile=… — the whole editor panel.
func (h *ResumeHandler) ResumeEditor(w http.ResponseWriter, r *http.Request) {
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))
	view, err := h.buildEditorView(r, profile)
	if err != nil {
		http.Error(w, "resume load: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "resume_editor", view)
}

// SaveBullet handles POST /ui/resume/bullets/{roleID}/{bulletID} — edit text.
func (h *ResumeHandler) SaveBullet(w http.ResponseWriter, r *http.Request) {
	roleID := chi.URLParam(r, "roleID")
	bulletID := chi.URLParam(r, "bulletID")
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))
	text := r.FormValue("text")

	if err := h.Resumes.UpsertBulletText(r.Context(), profile, roleID, bulletID, text); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "resume_bullet", resumeBulletEditView{
		Profile: profile, RoleID: roleID, BulletID: bulletID, Text: text,
	})
}

// AddBullet handles POST /ui/resume/bullets/{roleID} — append a new bullet.
// Returns the new bullet's row, which the add-form swaps in before itself.
func (h *ResumeHandler) AddBullet(w http.ResponseWriter, r *http.Request) {
	roleID := chi.URLParam(r, "roleID")
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))
	text := r.FormValue("text")

	bulletID, err := h.Resumes.AddBullet(r.Context(), profile, roleID, text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "resume_bullet", resumeBulletEditView{
		Profile: profile, RoleID: roleID, BulletID: bulletID, Text: text,
	})
}

// RemoveBullet handles POST /ui/resume/bullets/{roleID}/{bulletID}/retire.
// Soft-deletes; the row is removed from the DOM by swapping in nothing.
func (h *ResumeHandler) RemoveBullet(w http.ResponseWriter, r *http.Request) {
	roleID := chi.URLParam(r, "roleID")
	bulletID := chi.URLParam(r, "bulletID")
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))

	if err := h.Resumes.RetireBullet(r.Context(), profile, roleID, bulletID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Empty 200 body + hx-swap=outerHTML on the caller removes the row.
	w.WriteHeader(http.StatusOK)
}

func (h *ResumeHandler) buildEditorView(r *http.Request, profile string) (resumeEditorView, error) {
	res, err := resume.LoadProfile(r.Context(), h.Pool, profile)
	if err != nil {
		return resumeEditorView{}, err
	}
	view := resumeEditorView{Profile: profile}
	roleIdx := map[string]int{}
	for _, b := range res.Bullets {
		idx, ok := roleIdx[b.RoleID]
		if !ok {
			view.Roles = append(view.Roles, resumeRoleEditView{
				RoleID: b.RoleID, Title: b.RoleTitle, Company: b.RoleCompany, Dates: b.RoleDates,
			})
			idx = len(view.Roles) - 1
			roleIdx[b.RoleID] = idx
		}
		view.Roles[idx].Bullets = append(view.Roles[idx].Bullets, resumeBulletEditView{
			Profile: profile, RoleID: b.RoleID, BulletID: b.BulletID, Text: b.Text,
		})
	}
	return view, nil
}
