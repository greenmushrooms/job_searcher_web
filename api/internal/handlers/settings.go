package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
	"github.com/greenmushrooms/job_searcher_web/api/internal/searchconfig"
)

// spareRows is how many blank entry rows the form renders below the saved
// ones. The page's add/remove buttons handle row management; one server-side
// spare keeps an empty profile (and noscript) editable.
const spareRows = 1

// SettingsHandler serves the per-profile search editor. Each entry is one
// scrape — title + location + how many results to request — stored as rows in
// web.job_searches. The morning box-db-sync pull propagates rows to the
// pipeline's adm.job_searches, so edits take effect at the next 18:00 scrape.
// The sum of all entries' results is capped at MaxJobsPerRun.
type SettingsHandler struct {
	Config   *searchconfig.Repo
	Pool     *pgxpool.Pool
	Renderer *render.Renderer
}

type settingsRow struct {
	Title    string
	Location string
	Searches string // string so a blank spare row renders empty, not 0
}

type settingsView struct {
	Profile   string
	Rows      []settingsRow
	Total     int
	Max       int
	MaxRows   int
	UpdatedAt string
	Saved     bool
	Error     string
}

func settingsRows(entries []searchconfig.Entry) []settingsRow {
	rows := make([]settingsRow, 0, len(entries)+spareRows)
	for _, e := range entries {
		rows = append(rows, settingsRow{Title: e.Title, Location: e.Location, Searches: strconv.Itoa(e.Searches)})
	}
	for i := 0; i < spareRows; i++ {
		rows = append(rows, settingsRow{})
	}
	return rows
}

func (h *SettingsHandler) view(c *searchconfig.Config, saved bool, errMsg string) settingsView {
	return settingsView{
		Profile:   c.Profile,
		Rows:      settingsRows(c.Entries),
		Total:     c.Total(),
		Max:       searchconfig.MaxJobsPerRun,
		MaxRows:   searchconfig.MaxEntries,
		UpdatedAt: c.UpdatedAt,
		Saved:     saved,
		Error:     errMsg,
	}
}

// Page handles GET /settings.
func (h *SettingsHandler) Page(w http.ResponseWriter, r *http.Request) {
	profile := profiles.Resolve(r.Context(), r.URL.Query().Get("profile"))
	c, err := h.Config.Get(r.Context(), profile)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Renderer.HTML(w, http.StatusOK, "settings", h.view(c, false, ""))
}

// Save handles POST /settings. The form posts parallel arrays (one element
// per rendered row); rows left fully blank are spares and are skipped.
func (h *SettingsHandler) Save(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "bad form")
		return
	}
	profile := profiles.Resolve(r.Context(), r.FormValue("profile"))

	c := &searchconfig.Config{Profile: profile}
	titles, locations, counts := r.Form["title"], r.Form["location"], r.Form["searches"]
	at := func(ss []string, i int) string {
		if i < len(ss) {
			return strings.TrimSpace(ss[i])
		}
		return ""
	}

	n := len(titles)
	if len(locations) > n {
		n = len(locations)
	}
	var errMsg string
	for i := 0; i < n; i++ {
		title, loc, cnt := at(titles, i), at(locations, i), at(counts, i)
		if title == "" && loc == "" {
			continue // spare row
		}
		switch {
		case title == "" || loc == "":
			errMsg = fmt.Sprintf("row %d needs both a title and a location", i+1)
		case len(title) > 80 || len(loc) > 80:
			errMsg = fmt.Sprintf("row %d: entries must be under 80 characters", i+1)
		}
		searches := 20
		if cnt != "" {
			v, err := strconv.Atoi(cnt)
			if err != nil || v < 1 {
				errMsg = fmt.Sprintf("row %d: results must be a number of at least 1", i+1)
			} else {
				searches = v
			}
		}
		if errMsg != "" {
			break
		}
		c.Entries = append(c.Entries, searchconfig.Entry{Title: title, Location: loc, Searches: searches})
	}
	c.Dedupe()

	if errMsg == "" {
		switch {
		case len(c.Entries) == 0:
			errMsg = "add at least one search (title + location)"
		case len(c.Entries) > searchconfig.MaxEntries:
			errMsg = fmt.Sprintf("at most %d searches", searchconfig.MaxEntries)
		case c.Total() > searchconfig.MaxJobsPerRun:
			errMsg = fmt.Sprintf("results add up to %d — the daily cap is %d, lower some counts",
				c.Total(), searchconfig.MaxJobsPerRun)
		}
	}
	if errMsg != "" {
		h.Renderer.HTML(w, http.StatusOK, "settings", h.view(c, false, errMsg))
		return
	}

	if err := h.Config.Save(r.Context(), h.Pool, c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	saved, err := h.Config.Get(r.Context(), profile) // re-read for updated_at
	if err != nil {
		saved = c
	}
	h.Renderer.HTML(w, http.StatusOK, "settings", h.view(saved, true, ""))
}
