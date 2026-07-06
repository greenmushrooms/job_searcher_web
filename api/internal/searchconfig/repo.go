// Package searchconfig stores each profile's scrape criteria — the search
// titles, locations, and per-search result count the daily pipeline run uses.
// The web app edits web.job_search_config; the box-db-sync flow propagates it
// to the pipeline's adm.job_search_config on the homelab each morning, so a
// change here lands in the next 18:00 scrape.
package searchconfig

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

// MaxJobsPerRun caps titles × locations × searches — the total results one
// daily scrape may request for a profile.
const MaxJobsPerRun = 300

// MaxTerms bounds either list so a config stays scrape-able.
const MaxTerms = 10

type Config struct {
	Profile   string
	Titles    []string
	Locations []string
	Searches  int // results requested per title×location pair
	UpdatedAt string
}

// Total is the number of job results one daily run will request.
func (c Config) Total() int {
	return len(c.Titles) * len(c.Locations) * c.Searches
}

// ClampSearches lowers Searches so Total() fits under MaxJobsPerRun.
// Returns true when a clamp was applied.
func (c *Config) ClampSearches() bool {
	pairs := len(c.Titles) * len(c.Locations)
	if pairs == 0 {
		return false
	}
	max := MaxJobsPerRun / pairs
	if max < 1 {
		max = 1
	}
	if c.Searches > max {
		c.Searches = max
		return true
	}
	return false
}

var ErrNotFound = errors.New("no search config for profile")

type Repo struct {
	q db.Querier
}

func New(q db.Querier) *Repo { return &Repo{q: q} }

func (r *Repo) Get(ctx context.Context, profile string) (*Config, error) {
	c := Config{Profile: profile}
	err := r.q.QueryRow(ctx, `
        SELECT titles, locations, searches, updated_at::text
        FROM web.job_search_config
        WHERE sys_profile = $1`,
		profile,
	).Scan(&c.Titles, &c.Locations, &c.Searches, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *Repo) Save(ctx context.Context, c *Config) error {
	_, err := r.q.Exec(ctx, `
        INSERT INTO web.job_search_config (sys_profile, titles, locations, searches, updated_at)
        VALUES ($1, $2, $3, $4, now())
        ON CONFLICT (sys_profile) DO UPDATE
           SET titles = EXCLUDED.titles,
               locations = EXCLUDED.locations,
               searches = EXCLUDED.searches,
               updated_at = now()`,
		c.Profile, c.Titles, c.Locations, c.Searches)
	return err
}
