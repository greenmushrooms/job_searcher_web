// Package templates owns web.resume_templates + web.resume_template_bullets:
// reusable resume variants under a person. A template is a curated selection of
// canonical bullets with optional per-bullet text overrides (NULL = linked to
// the live canonical text). The virtual "Default" (full canonical pool) is not
// stored here — it's synthesised by the handlers/resume loader.
package templates

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Template struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	CreatedAt string `json:"created_at"`
}

// Bullet is one entry in a template: a reference to a canonical bullet plus an
// optional text override. OverrideText nil = use the live canonical text.
type Bullet struct {
	RoleID       string
	BulletID     string
	OverrideText *string
	SortOrder    int
}

// Repo holds the pool directly (not db.Querier) because Save/SetDefault need a
// transaction.
type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

var ErrNotFound = errors.New("template not found")

// List returns a profile's stored templates, default first then by name.
func (r *Repo) List(ctx context.Context, profile string) ([]Template, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT template_id, name, is_default, created_at::text
		FROM web.resume_templates
		WHERE sys_profile = $1
		ORDER BY is_default DESC, lower(name)`, profile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Template
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.Name, &t.IsDefault, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Save creates a new template from a bullet selection and returns its id.
func (r *Repo) Save(ctx context.Context, profile, name string, bullets []Bullet) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("template name required")
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO web.resume_templates (sys_profile, template_id, name)
		VALUES ($1, $2, $3)`, profile, id, name); err != nil {
		return "", fmt.Errorf("insert template: %w", err)
	}
	for _, b := range bullets {
		if _, err := tx.Exec(ctx, `
			INSERT INTO web.resume_template_bullets
				(sys_profile, template_id, role_id, bullet_id, override_text, sort_order)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			profile, id, b.RoleID, b.BulletID, b.OverrideText, b.SortOrder); err != nil {
			return "", fmt.Errorf("insert template bullet: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

// SaveMarkdown creates a new markdown template (name + body) and returns its
// id. Used by the markdown-centric flow where a template is a free-form resume
// document rather than a structured bullet selection.
func (r *Repo) SaveMarkdown(ctx context.Context, profile, name, markdown string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("template name required")
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO web.resume_templates (sys_profile, template_id, name, markdown)
		VALUES ($1, $2, $3, $4)`, profile, id, name, markdown); err != nil {
		return "", fmt.Errorf("insert markdown template: %w", err)
	}
	return id, nil
}

// ReplaceMarkdown overwrites an existing template's markdown body.
func (r *Repo) ReplaceMarkdown(ctx context.Context, profile, id, markdown string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE web.resume_templates SET markdown = $3, updated_at = NOW()
		WHERE sys_profile = $1 AND template_id = $2`, profile, id, markdown)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetMarkdown returns a template's stored markdown body, or "" if the template
// has none (e.g. a legacy structured template) or does not exist.
func (r *Repo) GetMarkdown(ctx context.Context, profile, id string) (string, error) {
	var md *string
	err := r.pool.QueryRow(ctx, `
		SELECT markdown FROM web.resume_templates
		WHERE sys_profile = $1 AND template_id = $2`, profile, id).Scan(&md)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if md == nil {
		return "", nil
	}
	return *md, nil
}

// Rename changes a template's display name.
func (r *Repo) Rename(ctx context.Context, profile, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("template name required")
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE web.resume_templates SET name = $3, updated_at = NOW()
		WHERE sys_profile = $1 AND template_id = $2`, profile, id, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a template (cascading to its bullets).
func (r *Repo) Delete(ctx context.Context, profile, id string) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM web.resume_templates
		WHERE sys_profile = $1 AND template_id = $2`, profile, id)
	return err
}

// SetDefault marks one template as the profile's default (clearing any other).
func (r *Repo) SetDefault(ctx context.Context, profile, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE web.resume_templates SET is_default = false
		WHERE sys_profile = $1 AND is_default`, profile); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE web.resume_templates SET is_default = true, updated_at = NOW()
		WHERE sys_profile = $1 AND template_id = $2`, profile, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func newID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("gen template id: %w", err)
	}
	return "t_" + hex.EncodeToString(b[:]), nil
}
