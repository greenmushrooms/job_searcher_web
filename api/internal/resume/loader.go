// Package resume loads the resume_data.json owned by the sibling resume_htmx
// project. Path comes from RESUME_JSON_PATH env (default
// ../../resume_htmx/resume_data.json relative to the api dir). The file is
// reloaded on each Load() — small enough that an mtime cache isn't worth it,
// and reloads pick up live edits from the resume_htmx UI without restart.
package resume

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

type Bullet struct {
	RoleID       string   `json:"role_id"`
	RoleTitle    string   `json:"role_title"`
	RoleCompany  string   `json:"role_company"`
	RoleDates    string   `json:"role_dates"`
	BulletID     string   `json:"bullet_id"`
	Text         string   `json:"text"`
	Tags         []string `json:"tags"`
}

// CompositeID returns "role_id.bullet_id" — the form used in DeepSeek prompts
// and in web.resume_finalizations.kept_bullet_ids.
func (b Bullet) CompositeID() string { return b.RoleID + "." + b.BulletID }

type Resume struct {
	SchemaVersion int      `json:"schema_version"`
	Bullets       []Bullet `json:"bullets"`
	Hash          string   `json:"hash"`    // sha256 of the file contents
	Version       string   `json:"version"` // "v{schema}-{hash[:8]}", what we pin into resume_finalizations
}

// raw mirrors the resume_data.json shape just enough to extract what we need.
type raw struct {
	Meta struct {
		SchemaVersion int `json:"schema_version"`
	} `json:"meta"`
	Experience []struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Company string `json:"company"`
		Dates   string `json:"dates"`
		Retired bool   `json:"retired"`
		Bullets map[string]struct {
			Text    string   `json:"text"`
			Tags    []string `json:"tags"`
			Retired bool     `json:"retired"`
		} `json:"bullets"`
	} `json:"experience"`
}

func defaultPath() string {
	if p := os.Getenv("RESUME_JSON_PATH"); p != "" {
		return p
	}
	return "../../resume_htmx/resume_data.json"
}

func Load() (*Resume, error) {
	path := defaultPath()
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var r raw
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])

	// Iterate roles in file order (matters: prompt readability + UI order).
	// json.Unmarshal preserves array order for slices, but `bullets` is a map,
	// so per-role bullet order is non-deterministic. Roles' bullet order in
	// the JSON is also a map. We sort bullet IDs alphabetically inside each
	// role for determinism — same input always produces the same prompt.
	out := &Resume{
		SchemaVersion: r.Meta.SchemaVersion,
		Hash:          hash,
		Version:       fmt.Sprintf("v%d-%s", r.Meta.SchemaVersion, hash[:8]),
	}
	for _, role := range r.Experience {
		if role.Retired {
			continue
		}
		keys := make([]string, 0, len(role.Bullets))
		for k := range role.Bullets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, bk := range keys {
			b := role.Bullets[bk]
			if b.Retired {
				continue
			}
			out.Bullets = append(out.Bullets, Bullet{
				RoleID:      role.ID,
				RoleTitle:   role.Title,
				RoleCompany: role.Company,
				RoleDates:   role.Dates,
				BulletID:    bk,
				Text:        b.Text,
				Tags:        b.Tags,
			})
		}
	}
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

