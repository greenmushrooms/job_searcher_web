// Package searchconfig stores each profile's scrape criteria as individual
// search entries — one row per (title, location, results) scrape the daily
// pipeline runs. The web app edits web.job_searches; the box-db-sync flow
// propagates rows to the pipeline's adm.job_searches on the homelab each
// morning, so a change here lands in the next 18:00 scrape.
package searchconfig

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

// MaxJobsPerRun caps sum(searches) — the total results one daily scrape may
// request for a profile.
const MaxJobsPerRun = 300

// MaxEntries bounds the list so a config stays scrape-able.
const MaxEntries = 20

// Entry is one scrape: this title, in this location, this many results.
type Entry struct {
	Title    string
	Location string
	Searches int
}

type Config struct {
	Profile   string
	Entries   []Entry
	UpdatedAt string // latest row's save date, "" when never saved
}

// Total is the number of job results one daily run will request.
func (c Config) Total() int {
	n := 0
	for _, e := range c.Entries {
		n += e.Searches
	}
	return n
}

// Dedupe drops repeated (title, location) pairs case-insensitively, keeping
// the first occurrence — a duplicate row would just scrape the same results
// twice.
func (c *Config) Dedupe() {
	seen := map[string]bool{}
	out := c.Entries[:0]
	for _, e := range c.Entries {
		k := strings.ToLower(strings.TrimSpace(e.Title)) + "\x00" + strings.ToLower(strings.TrimSpace(e.Location))
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	c.Entries = out
}

type Repo struct {
	q db.Querier
}

func New(q db.Querier) *Repo { return &Repo{q: q} }

// Get returns the profile's entries in saved order. A profile with no rows
// yields an empty (not nil-error) config — the editor renders blank rows.
func (r *Repo) Get(ctx context.Context, profile string) (*Config, error) {
	c := Config{Profile: profile}
	rows, err := r.q.Query(ctx, `
        SELECT title, location, searches, max(updated_at::date::text) OVER ()
        FROM web.job_searches
        WHERE sys_profile = $1
        ORDER BY sort_order`,
		profile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Title, &e.Location, &e.Searches, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Entries = append(c.Entries, e)
	}
	return &c, rows.Err()
}

// Save replaces the profile's entries in one transaction — the form posts the
// whole list, so replace-all keeps sort_order dense and deletions implicit.
func (r *Repo) Save(ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, c *Config) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`DELETE FROM web.job_searches WHERE sys_profile = $1`, c.Profile); err != nil {
		return err
	}
	for i, e := range c.Entries {
		if _, err := tx.Exec(ctx, `
            INSERT INTO web.job_searches (sys_profile, sort_order, title, location, searches, updated_at)
            VALUES ($1, $2, $3, $4, $5, now())`,
			c.Profile, i, e.Title, e.Location, e.Searches); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
