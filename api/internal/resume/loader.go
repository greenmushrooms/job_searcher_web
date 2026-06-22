// Package resume loads the canonical resume from the DB (web.user_profile +
// web.resume_*), which job_searcher_web now owns. It used to read the flat
// resume_data.json owned by resume_htmx; that file is now just the import
// source for cmd/seed-resume.
//
// The resume is single-owner for now: RESUME_PROFILE (default "Slava") selects
// whose resume every draft tailors against, independent of the audit-log
// profile a draft is recorded under. Per-profile resume loading arrives when
// the other profiles' resumes are seeded.
package resume

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

// DefaultTemplateID is the virtual "Default" template: the full active
// canonical bullet pool, with no stored selection or overrides.
const DefaultTemplateID = "default"

type Bullet struct {
	RoleID      string   `json:"role_id"`
	RoleTitle   string   `json:"role_title"`
	RoleCompany string   `json:"role_company"`
	RoleDates   string   `json:"role_dates"`
	BulletID    string   `json:"bullet_id"`
	Text        string   `json:"text"`
	Tags        []string `json:"tags"`
}

// CompositeID returns "role_id.bullet_id" — the form used in DeepSeek prompts
// and in web.resume_finalizations.kept_bullet_ids.
func (b Bullet) CompositeID() string { return b.RoleID + "." + b.BulletID }

type Resume struct {
	SchemaVersion int      `json:"schema_version"`
	Bullets       []Bullet `json:"bullets"`
	Hash          string   `json:"hash"`    // sha256 of the bullet content
	Version       string   `json:"version"` // "v{schema}-{hash[:8]}", what we pin into resume_finalizations
}

func defaultProfile() string {
	if p := os.Getenv("RESUME_PROFILE"); p != "" {
		return p
	}
	return "Slava"
}

// Load reads the resume for the configured RESUME_PROFILE.
func Load(ctx context.Context, q db.Querier) (*Resume, error) {
	return LoadProfile(ctx, q, defaultProfile())
}

// LoadProfile reads active (non-retired) bullets for one profile, joined with
// their role metadata, in (role, bullet) sort order — the order the editor and
// prompt present them in. Version is derived from the bullet content so a pin
// in web.resume_finalizations still tells current from stale selections, the
// same role the file hash used to play.
func LoadProfile(ctx context.Context, q db.Querier, profile string) (*Resume, error) {
	rows, err := q.Query(ctx, `
		SELECT b.role_id, r.title, r.company, r.dates, b.bullet_id, b.text, b.tags
		FROM web.resume_bullets b
		JOIN web.resume_roles r
		  ON r.sys_profile = b.sys_profile AND r.role_id = b.role_id
		WHERE b.sys_profile = $1 AND NOT b.retired AND NOT r.retired
		ORDER BY r.sort_order, b.sort_order, b.bullet_id`, profile)
	if err != nil {
		return nil, fmt.Errorf("query bullets: %w", err)
	}
	defer rows.Close()
	bullets, err := scanBullets(rows)
	if err != nil {
		return nil, err
	}
	return newResume(schemaVersionFor(ctx, q, profile), bullets), nil
}

// LoadTemplate loads the bullet pool for a named resume template, applying each
// row's override_text over the canonical bullet (NULL override = live canonical
// text, the "linked" behaviour). The virtual DefaultTemplateID falls back to
// the full canonical pool. Canonical-retired bullets/roles are excluded, so a
// template never resurrects a retired bullet.
func LoadTemplate(ctx context.Context, q db.Querier, profile, templateID string) (*Resume, error) {
	if templateID == "" || templateID == DefaultTemplateID {
		return LoadProfile(ctx, q, profile)
	}
	rows, err := q.Query(ctx, `
		SELECT b.role_id, r.title, r.company, r.dates, b.bullet_id,
		       COALESCE(tb.override_text, b.text) AS text, b.tags
		FROM web.resume_template_bullets tb
		JOIN web.resume_bullets b
		  ON b.sys_profile = tb.sys_profile AND b.role_id = tb.role_id AND b.bullet_id = tb.bullet_id
		JOIN web.resume_roles r
		  ON r.sys_profile = b.sys_profile AND r.role_id = b.role_id
		WHERE tb.sys_profile = $1 AND tb.template_id = $2
		  AND NOT b.retired AND NOT r.retired
		ORDER BY r.sort_order, tb.sort_order, b.bullet_id`, profile, templateID)
	if err != nil {
		return nil, fmt.Errorf("query template bullets: %w", err)
	}
	defer rows.Close()
	bullets, err := scanBullets(rows)
	if err != nil {
		return nil, err
	}
	return newResume(schemaVersionFor(ctx, q, profile), bullets), nil
}

// schemaVersionFor reads schema_version off the profile row. Absence just means
// "not seeded" — fall back to the current schema rather than failing.
func schemaVersionFor(ctx context.Context, q db.Querier, profile string) int {
	var v int
	if err := q.QueryRow(ctx,
		`SELECT schema_version FROM web.user_profile WHERE sys_profile = $1`, profile,
	).Scan(&v); err != nil {
		return 2
	}
	return v
}

// scanBullets reads (role, bullet) rows in the column order the loader queries
// use. Callers fix the ORDER BY, so the slice order is deterministic.
func scanBullets(rows pgx.Rows) ([]Bullet, error) {
	var out []Bullet
	for rows.Next() {
		var b Bullet
		if err := rows.Scan(&b.RoleID, &b.RoleTitle, &b.RoleCompany, &b.RoleDates,
			&b.BulletID, &b.Text, &b.Tags); err != nil {
			return nil, fmt.Errorf("scan bullet: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bullets: %w", err)
	}
	return out, nil
}

// newResume fingerprints the bullet content (order fixed by the query) so a pin
// in jobs_resume can tell a current selection from a stale one.
func newResume(schemaVersion int, bullets []Bullet) *Resume {
	var content strings.Builder
	for _, b := range bullets {
		content.WriteString(b.CompositeID())
		content.WriteByte('\t')
		content.WriteString(b.Text)
		content.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(content.String()))
	hash := hex.EncodeToString(sum[:])
	return &Resume{
		SchemaVersion: schemaVersion,
		Bullets:       bullets,
		Hash:          hash,
		Version:       fmt.Sprintf("v%d-%s", schemaVersion, hash[:8]),
	}
}
