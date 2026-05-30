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

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

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
	var schemaVersion int
	// schema_version lives on the profile row; absence just means "not seeded"
	// — fall back to the current schema rather than failing the draft.
	if err := q.QueryRow(ctx,
		`SELECT schema_version FROM web.user_profile WHERE sys_profile = $1`, profile,
	).Scan(&schemaVersion); err != nil {
		schemaVersion = 2
	}

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

	out := &Resume{SchemaVersion: schemaVersion}
	var content strings.Builder
	for rows.Next() {
		var b Bullet
		if err := rows.Scan(&b.RoleID, &b.RoleTitle, &b.RoleCompany, &b.RoleDates,
			&b.BulletID, &b.Text, &b.Tags); err != nil {
			return nil, fmt.Errorf("scan bullet: %w", err)
		}
		out.Bullets = append(out.Bullets, b)
		// Deterministic content fingerprint: order matters and is fixed by the
		// query's ORDER BY, so the same bullets always hash the same.
		content.WriteString(b.CompositeID())
		content.WriteByte('\t')
		content.WriteString(b.Text)
		content.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bullets: %w", err)
	}

	sum := sha256.Sum256([]byte(content.String()))
	out.Hash = hex.EncodeToString(sum[:])
	out.Version = fmt.Sprintf("v%d-%s", schemaVersion, out.Hash[:8])
	return out, nil
}

// Lookup finds a bullet by composite ID. Returns nil if not found.
func (r *Resume) Lookup(compositeID string) *Bullet {
	for i := range r.Bullets {
		if r.Bullets[i].CompositeID() == compositeID {
			return &r.Bullets[i]
		}
	}
	return nil
}
