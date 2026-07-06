package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
	"github.com/greenmushrooms/job_searcher_web/api/internal/searchconfig"
)

// SettingsHandler serves the per-profile search-criteria editor. Edits land in
// web.job_search_config; the morning box-db-sync pull propagates them to the
// pipeline's adm.job_search_config, so they take effect at the next 18:00
// scrape. Searches is clamped so titles × locations × searches ≤ 300.
type SettingsHandler struct {
	Config   *searchconfig.Repo
	Renderer *render.Renderer
}

type settingsView struct {
	Profile   string
	Titles    string // one per line
	Locations string
	Searches  int
	Total     int
	Max       int
	UpdatedAt string
	Saved     bool
	Clamped   bool
	Error     string
}

func (h *SettingsHandler) view(c *searchconfig.Config, saved, clamped bool, errMsg string) settingsView {
	return settingsView{
		Profile:   c.Profile,
		Titles:    strings.Join(c.Titles, "\n"),
		Locations: strings.Join(c.Locations, "\n"),
		Searches:  c.Searches,
		Total:     c.Total(),
		Max:       searchconfig.MaxJobsPerRun,
		UpdatedAt: dateOnly(&c.UpdatedAt),
		Saved:     saved,
		Clamped:   clamped,
		Error:     errMsg,
	}
}

// Page handles GET /settings.
func (h *SettingsHandler) Page(w http.ResponseWriter, r *http.Request) {
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))
	c, err := h.Config.Get(r.Context(), profile)
	if err == searchconfig.ErrNotFound {
		c = &searchconfig.Config{Profile: profile, Searches: 20}
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "settings", h.view(c, false, false, ""))
}

// Save handles POST /settings.
func (h *SettingsHandler) Save(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "bad form")
		return
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))

	c := &searchconfig.Config{
		Profile:   profile,
		Titles:    splitLines(r.FormValue("titles")),
		Locations: splitLines(r.FormValue("locations")),
	}
	if n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("searches"))); err == nil {
		c.Searches = n
	}

	if msg := validateConfig(c); msg != "" {
		h.Renderer.HTML(w, http.StatusOK, "settings", h.view(c, false, false, msg))
		return
	}
	clamped := c.ClampSearches()
	if err := h.Config.Save(r.Context(), c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	saved, err := h.Config.Get(r.Context(), profile) // re-read for updated_at
	if err != nil {
		saved = c
	}
	h.Renderer.HTML(w, http.StatusOK, "settings", h.view(saved, true, clamped, ""))
}

func validateConfig(c *searchconfig.Config) string {
	if len(c.Titles) == 0 {
		return "add at least one search title"
	}
	if len(c.Locations) == 0 {
		return "add at least one location"
	}
	if len(c.Titles) > searchconfig.MaxTerms {
		return fmt.Sprintf("at most %d titles", searchconfig.MaxTerms)
	}
	if len(c.Locations) > searchconfig.MaxTerms {
		return fmt.Sprintf("at most %d locations", searchconfig.MaxTerms)
	}
	for _, s := range append(append([]string{}, c.Titles...), c.Locations...) {
		if len(s) > 80 {
			return "each entry must be under 80 characters"
		}
	}
	if c.Searches < 1 {
		return "results per search must be at least 1"
	}
	return ""
}

// splitLines turns textarea input into a trimmed, de-duplicated list.
func splitLines(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[strings.ToLower(line)] {
			continue
		}
		seen[strings.ToLower(line)] = true
		out = append(out, line)
	}
	return out
}
