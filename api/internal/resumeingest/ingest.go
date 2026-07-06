// Package resumeingest turns an LLM-structured résumé into the canonical
// per-profile tables. It is the web onboarding path for a NEW résumé —
// the counterpart of cmd/seed-resume's file import — and writes BOTH stores
// the app keeps: the structured web.user_profile/resume_* rows the tailoring
// pipeline reads, and the free-form web.resume_master markdown the editors use.
//
// Replace is destructive by design (a new résumé supersedes the old one), so
// the handler must hold an explicit user confirmation before calling it on a
// profile that already has data.
package resumeingest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Structured mirrors the JSON contract in deepseek's ingest prompt (and the
// shape of cmd/seed-resume's resume_data.json, minus ids — ids are generated
// here so the ingested résumé gets the same role_id.bullet_id addressing the
// tailoring flow relies on).
type Structured struct {
	Contact struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Phone    string `json:"phone"`
		GitHub   string `json:"github"`
		Location string `json:"location"`
	} `json:"contact"`
	Summary string `json:"summary"`
	Skills  []struct {
		Text     string `json:"text"`
		Category string `json:"category"`
	} `json:"skills"`
	Experience []struct {
		Title    string `json:"title"`
		Company  string `json:"company"`
		Location string `json:"location"`
		Dates    string `json:"dates"`
		Bullets  []struct {
			Text string `json:"text"`
		} `json:"bullets"`
	} `json:"experience"`
	Education []struct {
		Degree      string `json:"degree"`
		Institution string `json:"institution"`
		Location    string `json:"location"`
	} `json:"education"`
	Markdown string `json:"markdown"`
}

// Parse decodes the LLM output and sanity-checks it enough to store.
func Parse(raw json.RawMessage) (*Structured, error) {
	var s Structured
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("decode structured résumé: %w", err)
	}
	if len(s.Experience) == 0 {
		return nil, fmt.Errorf("no work experience found — is this a résumé?")
	}
	if len(s.Experience) > 30 {
		return nil, fmt.Errorf("%d roles parsed — that doesn't look like one résumé", len(s.Experience))
	}
	bullets := 0
	for _, r := range s.Experience {
		if strings.TrimSpace(r.Title) == "" && strings.TrimSpace(r.Company) == "" {
			return nil, fmt.Errorf("a role is missing both title and company")
		}
		bullets += len(r.Bullets)
	}
	if bullets == 0 {
		return nil, fmt.Errorf("no bullet points found under any role")
	}
	return &s, nil
}

// Counts summarizes what an ingest wrote, for the confirmation page.
type Counts struct {
	Roles, Bullets, Skills, Education int
	MasterBytes                       int
}

// HasExisting reports whether the profile already has résumé data in either
// store — used by the handler to demand explicit replace confirmation.
func HasExisting(ctx context.Context, q querier, profile string) (bool, error) {
	var n int
	err := q.QueryRow(ctx, `
        SELECT (SELECT count(*) FROM web.resume_roles WHERE sys_profile = $1)
             + (SELECT count(*) FROM web.resume_master WHERE sys_profile = $1)`,
		profile,
	).Scan(&n)
	return n > 0, err
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Replace swaps in the new résumé for a profile in one transaction: existing
// structured rows are deleted, the parsed rows inserted with fresh generated
// ids, and resume_master set to the parsed markdown (or the raw pasted text
// when the model returned none). All-or-nothing — a failure leaves the
// profile's previous résumé untouched.
func Replace(ctx context.Context, pool beginner, profile, pastedText string, s *Structured) (Counts, error) {
	var c Counts
	tx, err := pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(ctx)

	// resume_bullets cascades from resume_roles.
	for _, del := range []string{
		`DELETE FROM web.resume_roles     WHERE sys_profile = $1`,
		`DELETE FROM web.resume_skills    WHERE sys_profile = $1`,
		`DELETE FROM web.resume_education WHERE sys_profile = $1`,
	} {
		if _, err := tx.Exec(ctx, del, profile); err != nil {
			return c, fmt.Errorf("clear existing: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
        INSERT INTO web.user_profile (sys_profile, name, email, phone, github, location, summary, schema_version, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,2,NOW())
        ON CONFLICT (sys_profile) DO UPDATE SET
            name=EXCLUDED.name, email=EXCLUDED.email, phone=EXCLUDED.phone,
            github=EXCLUDED.github, location=EXCLUDED.location, summary=EXCLUDED.summary,
            updated_at=NOW()`,
		profile, s.Contact.Name, s.Contact.Email, s.Contact.Phone,
		s.Contact.GitHub, s.Contact.Location, s.Summary,
	); err != nil {
		return c, fmt.Errorf("user_profile: %w", err)
	}

	for i, sk := range s.Skills {
		if strings.TrimSpace(sk.Text) == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
            INSERT INTO web.resume_skills (sys_profile, skill_id, text, category, sort_order)
            VALUES ($1,$2,$3,$4,$5)`,
			profile, fmt.Sprintf("s%d", i+1), sk.Text, sk.Category, i,
		); err != nil {
			return c, fmt.Errorf("skill %d: %w", i+1, err)
		}
		c.Skills++
	}

	seen := map[string]bool{}
	for i, role := range s.Experience {
		roleID := uniqueSlug(role.Company, role.Title, i, seen)
		if _, err := tx.Exec(ctx, `
            INSERT INTO web.resume_roles (sys_profile, role_id, title, company, location, dates, sort_order)
            VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			profile, roleID, role.Title, role.Company, role.Location, role.Dates, i,
		); err != nil {
			return c, fmt.Errorf("role %s: %w", roleID, err)
		}
		c.Roles++
		for j, b := range role.Bullets {
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			if _, err := tx.Exec(ctx, `
                INSERT INTO web.resume_bullets (sys_profile, role_id, bullet_id, text, tags, sort_order)
                VALUES ($1,$2,$3,$4,'{}',$5)`,
				profile, roleID, fmt.Sprintf("b%d", j+1), b.Text, j,
			); err != nil {
				return c, fmt.Errorf("bullet %s.b%d: %w", roleID, j+1, err)
			}
			c.Bullets++
		}
	}

	for i, e := range s.Education {
		if strings.TrimSpace(e.Degree) == "" && strings.TrimSpace(e.Institution) == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
            INSERT INTO web.resume_education (sys_profile, education_id, degree, institution, location, sort_order)
            VALUES ($1,$2,$3,$4,$5,$6)`,
			profile, fmt.Sprintf("edu%d", i), e.Degree, e.Institution, e.Location, i,
		); err != nil {
			return c, fmt.Errorf("education %d: %w", i, err)
		}
		c.Education++
	}

	master := s.Markdown
	if strings.TrimSpace(master) == "" {
		master = pastedText
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO web.resume_master (sys_profile, markdown, updated_at)
        VALUES ($1,$2,NOW())
        ON CONFLICT (sys_profile) DO UPDATE
        SET markdown = EXCLUDED.markdown, updated_at = NOW()`,
		profile, master,
	); err != nil {
		return c, fmt.Errorf("resume_master: %w", err)
	}
	c.MasterBytes = len(master)

	return c, tx.Commit(ctx)
}

// uniqueSlug builds a stable, readable role_id (company first, title as
// fallback), deduped with a positional suffix — the id becomes half of the
// role_id.bullet_id contract used across tailoring, so keep it filesystem-ish.
func uniqueSlug(company, title string, i int, seen map[string]bool) string {
	base := slugify(company)
	if base == "" {
		base = slugify(title)
	}
	if base == "" {
		base = "role"
	}
	if len(base) > 24 {
		base = strings.Trim(base[:24], "_")
	}
	id := base
	if seen[id] {
		id = fmt.Sprintf("%s_%d", base, i+1)
	}
	seen[id] = true
	return id
}

func slugify(s string) string {
	var b strings.Builder
	prevUnder := true // trims leading separators
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnder = false
		default:
			if !prevUnder {
				b.WriteByte('_')
				prevUnder = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}
