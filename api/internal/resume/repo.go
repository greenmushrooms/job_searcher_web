package resume

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

// Repo owns writes to the canonical resume (the left-hand editor). Reads still
// go through the package-level Load/LoadProfile, which the draft flow shares.
type Repo struct {
	q db.Querier
}

func NewRepo(q db.Querier) *Repo { return &Repo{q: q} }

// UpsertBulletText edits one bullet's text in place. Tags are left untouched —
// the editor only writes text for now. Returns an error if the bullet doesn't
// exist (so a stale fragment can't silently no-op).
func (r *Repo) UpsertBulletText(ctx context.Context, profile, roleID, bulletID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("bullet text required")
	}
	tag, err := r.q.Exec(ctx, `
		UPDATE web.resume_bullets SET text = $4
		WHERE sys_profile = $1 AND role_id = $2 AND bullet_id = $3`,
		profile, roleID, bulletID, text)
	if err != nil {
		return fmt.Errorf("update bullet: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("bullet %s.%s not found for %s", roleID, bulletID, profile)
	}
	return nil
}

// AddBullet appends a new active bullet to a role and returns its generated
// bullet_id. sort_order is max+1 within the role so it lands at the end.
func (r *Repo) AddBullet(ctx context.Context, profile, roleID, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("bullet text required")
	}
	bulletID, err := newBulletID()
	if err != nil {
		return "", err
	}
	// SELECT-driven insert computes the next sort_order in one round-trip. The
	// FK to resume_roles rejects an unknown role, surfaced as an error.
	_, err = r.q.Exec(ctx, `
		INSERT INTO web.resume_bullets (sys_profile, role_id, bullet_id, text, sort_order)
		SELECT $1, $2, $3, $4, COALESCE(MAX(sort_order), -1) + 1
		FROM web.resume_bullets WHERE sys_profile = $1 AND role_id = $2`,
		profile, roleID, bulletID, text)
	if err != nil {
		return "", fmt.Errorf("insert bullet: %w", err)
	}
	return bulletID, nil
}

// RetireBullet soft-deletes a bullet (retired = true) so it drops out of the
// resume and the draft pool but stays referenceable by old finalizations.
func (r *Repo) RetireBullet(ctx context.Context, profile, roleID, bulletID string) error {
	tag, err := r.q.Exec(ctx, `
		UPDATE web.resume_bullets SET retired = true
		WHERE sys_profile = $1 AND role_id = $2 AND bullet_id = $3`,
		profile, roleID, bulletID)
	if err != nil {
		return fmt.Errorf("retire bullet: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("bullet %s.%s not found for %s", roleID, bulletID, profile)
	}
	return nil
}

// newBulletID returns a short random id like "b_3f9a2c1d". Random (not max+1)
// so it never collides with a retired bullet's id that's still on file.
func newBulletID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("gen bullet id: %w", err)
	}
	return "b_" + hex.EncodeToString(b[:]), nil
}
