// Command seed-resume applies migration 005 and imports the canonical resume
// from resume_data.json into web.user_profile + web.resume_* tables. It is
// idempotent (upserts), so re-running picks up edits made in resume_htmx.
//
//	go run ./cmd/seed-resume                 # profile=Slava, file from RESUME_JSON_PATH
//	go run ./cmd/seed-resume -profile Cait -file /path/to/resume_data.json
//
// Once the left-hand editor writes user_profile directly this becomes a
// one-time migration tool; until then it's how the DB stays in sync with the
// file resume_htmx still edits.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

// raw mirrors resume_data.json — the full document, not just the bullets the
// reader currently uses, so the import populates every canonical table.
type raw struct {
	Meta struct {
		SchemaVersion int `json:"schema_version"`
	} `json:"meta"`
	Contact struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Phone    string `json:"phone"`
		GitHub   string `json:"github"`
		Location string `json:"location"`
	} `json:"contact"`
	Summary string `json:"summary"`
	Skills  []struct {
		ID       string `json:"id"`
		Text     string `json:"text"`
		Category string `json:"category"`
	} `json:"skills"`
	Experience []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Company  string `json:"company"`
		Location string `json:"location"`
		Dates    string `json:"dates"`
		Retired  bool   `json:"retired"`
		Bullets  map[string]struct {
			Text    string   `json:"text"`
			Tags    []string `json:"tags"`
			Retired bool     `json:"retired"`
		} `json:"bullets"`
	} `json:"experience"`
	Education []struct {
		Degree      string `json:"degree"`
		Institution string `json:"institution"`
		Location    string `json:"location"`
	} `json:"education"`
}

func main() {
	profile := flag.String("profile", "Slava", "sys_profile to import the resume under")
	file := flag.String("file", "", "path to resume_data.json (default: RESUME_JSON_PATH)")
	migrationPath := flag.String("migration", "../migrations/005_user_profile.sql", "DDL applied before import")
	flag.Parse()

	for _, p := range []string{".env", "../.env", "../../.env"} {
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			break
		}
	}

	path := *file
	if path == "" {
		path = os.Getenv("RESUME_JSON_PATH")
	}
	if path == "" {
		log.Fatal("no resume file: pass -file or set RESUME_JSON_PATH")
	}

	body, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read %s: %v", path, err)
	}
	var r raw
	if err := json.Unmarshal(body, &r); err != nil {
		log.Fatalf("parse %s: %v", path, err)
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	// Apply the DDL first (CREATE TABLE IF NOT EXISTS — safe to re-run). pgx's
	// simple-protocol Exec runs the whole multi-statement file in one call.
	ddl, err := os.ReadFile(*migrationPath)
	if err != nil {
		log.Fatalf("read migration %s: %v", *migrationPath, err)
	}
	if _, err := pool.Exec(ctx, string(ddl)); err != nil {
		log.Fatalf("apply migration: %v", err)
	}

	if err := importResume(ctx, pool, *profile, &r); err != nil {
		log.Fatalf("import: %v", err)
	}

	var roles, bullets, skills, edu int
	_ = pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM web.resume_roles     WHERE sys_profile=$1),
		(SELECT count(*) FROM web.resume_bullets   WHERE sys_profile=$1),
		(SELECT count(*) FROM web.resume_skills    WHERE sys_profile=$1),
		(SELECT count(*) FROM web.resume_education WHERE sys_profile=$1)`,
		*profile).Scan(&roles, &bullets, &skills, &edu)
	fmt.Printf("imported profile=%s: %d roles, %d bullets, %d skills, %d education\n",
		*profile, roles, bullets, skills, edu)
}

func importResume(ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, profile string, r *raw) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO web.user_profile (sys_profile, name, email, phone, github, location, summary, schema_version, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8, NOW())
		ON CONFLICT (sys_profile) DO UPDATE SET
			name=EXCLUDED.name, email=EXCLUDED.email, phone=EXCLUDED.phone,
			github=EXCLUDED.github, location=EXCLUDED.location, summary=EXCLUDED.summary,
			schema_version=EXCLUDED.schema_version, updated_at=NOW()`,
		profile, r.Contact.Name, r.Contact.Email, r.Contact.Phone, r.Contact.GitHub,
		r.Contact.Location, r.Summary, r.Meta.SchemaVersion,
	); err != nil {
		return fmt.Errorf("user_profile: %w", err)
	}

	for i, s := range r.Skills {
		if _, err := tx.Exec(ctx, `
			INSERT INTO web.resume_skills (sys_profile, skill_id, text, category, sort_order)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (sys_profile, skill_id) DO UPDATE SET
				text=EXCLUDED.text, category=EXCLUDED.category, sort_order=EXCLUDED.sort_order`,
			profile, s.ID, s.Text, s.Category, i,
		); err != nil {
			return fmt.Errorf("skill %s: %w", s.ID, err)
		}
	}

	for i, role := range r.Experience {
		if _, err := tx.Exec(ctx, `
			INSERT INTO web.resume_roles (sys_profile, role_id, title, company, location, dates, sort_order, retired)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (sys_profile, role_id) DO UPDATE SET
				title=EXCLUDED.title, company=EXCLUDED.company, location=EXCLUDED.location,
				dates=EXCLUDED.dates, sort_order=EXCLUDED.sort_order, retired=EXCLUDED.retired`,
			profile, role.ID, role.Title, role.Company, role.Location, role.Dates, i, role.Retired,
		); err != nil {
			return fmt.Errorf("role %s: %w", role.ID, err)
		}

		// bullets is a JSON map (no inherent order); sort keys so sort_order is
		// deterministic — same rule the file loader used.
		keys := make([]string, 0, len(role.Bullets))
		for k := range role.Bullets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for j, bk := range keys {
			b := role.Bullets[bk]
			tags := b.Tags
			if tags == nil {
				tags = []string{}
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO web.resume_bullets (sys_profile, role_id, bullet_id, text, tags, sort_order, retired)
				VALUES ($1,$2,$3,$4,$5,$6,$7)
				ON CONFLICT (sys_profile, role_id, bullet_id) DO UPDATE SET
					text=EXCLUDED.text, tags=EXCLUDED.tags, sort_order=EXCLUDED.sort_order, retired=EXCLUDED.retired`,
				profile, role.ID, bk, b.Text, tags, j, b.Retired,
			); err != nil {
				return fmt.Errorf("bullet %s.%s: %w", role.ID, bk, err)
			}
		}
	}

	// education has no id in the file; synthesise a stable one from position.
	for i, e := range r.Education {
		id := fmt.Sprintf("edu_%d", i)
		if _, err := tx.Exec(ctx, `
			INSERT INTO web.resume_education (sys_profile, education_id, degree, institution, location, sort_order)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (sys_profile, education_id) DO UPDATE SET
				degree=EXCLUDED.degree, institution=EXCLUDED.institution,
				location=EXCLUDED.location, sort_order=EXCLUDED.sort_order`,
			profile, id, e.Degree, e.Institution, e.Location, i,
		); err != nil {
			return fmt.Errorf("education %s: %w", id, err)
		}
	}

	return tx.Commit(ctx)
}
