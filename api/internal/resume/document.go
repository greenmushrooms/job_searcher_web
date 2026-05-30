package resume

import (
	"context"
	"fmt"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

// Document is the full resume in the shape resume_htmx's PDF renderer expects
// (mirrors resume_data.json). LoadProfile returns just the bullet pool for the
// draft flow; this returns everything — contact, summary, skills, education —
// needed to render a complete PDF.
type Document struct {
	Contact    DocContact     `json:"contact"`
	Summary    string         `json:"summary"`
	Skills     []DocSkill     `json:"skills"`
	Experience []DocRole      `json:"experience"`
	Education  []DocEducation `json:"education"`
}

type DocContact struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Github   string `json:"github"`
	Location string `json:"location"`
}

type DocSkill struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Category string `json:"category"`
}

type DocRole struct {
	ID       string               `json:"id"`
	Title    string               `json:"title"`
	Company  string               `json:"company"`
	Location string               `json:"location"`
	Dates    string               `json:"dates"`
	Retired  bool                 `json:"retired"`
	Bullets  map[string]DocBullet `json:"bullets"`
}

type DocBullet struct {
	Text string   `json:"text"`
	Tags []string `json:"tags"`
}

type DocEducation struct {
	Degree      string `json:"degree"`
	Institution string `json:"institution"`
	Location    string `json:"location"`
}

// LoadDocument assembles the full resume for a profile from the DB, excluding
// retired roles/bullets/skills. Roles come out in sort order; bullets live in
// a map keyed by bullet_id (the PDF template renders them in selection order,
// so map ordering doesn't matter here).
func LoadDocument(ctx context.Context, q db.Querier, profile string) (*Document, error) {
	doc := &Document{
		Skills:     []DocSkill{},
		Experience: []DocRole{},
		Education:  []DocEducation{},
	}

	// contact + summary (singleton). Missing row → empty doc, not an error.
	_ = q.QueryRow(ctx, `
		SELECT name, email, phone, github, location, summary
		FROM web.user_profile WHERE sys_profile = $1`, profile,
	).Scan(&doc.Contact.Name, &doc.Contact.Email, &doc.Contact.Phone,
		&doc.Contact.Github, &doc.Contact.Location, &doc.Summary)

	skillRows, err := q.Query(ctx, `
		SELECT skill_id, text, category FROM web.resume_skills
		WHERE sys_profile = $1 AND NOT retired ORDER BY sort_order, skill_id`, profile)
	if err != nil {
		return nil, fmt.Errorf("query skills: %w", err)
	}
	for skillRows.Next() {
		var s DocSkill
		if err := skillRows.Scan(&s.ID, &s.Text, &s.Category); err != nil {
			skillRows.Close()
			return nil, err
		}
		doc.Skills = append(doc.Skills, s)
	}
	skillRows.Close()
	if err := skillRows.Err(); err != nil {
		return nil, err
	}

	roleRows, err := q.Query(ctx, `
		SELECT role_id, title, company, location, dates FROM web.resume_roles
		WHERE sys_profile = $1 AND NOT retired ORDER BY sort_order, role_id`, profile)
	if err != nil {
		return nil, fmt.Errorf("query roles: %w", err)
	}
	roleIdx := map[string]int{}
	for roleRows.Next() {
		var rl DocRole
		if err := roleRows.Scan(&rl.ID, &rl.Title, &rl.Company, &rl.Location, &rl.Dates); err != nil {
			roleRows.Close()
			return nil, err
		}
		rl.Bullets = map[string]DocBullet{}
		doc.Experience = append(doc.Experience, rl)
		roleIdx[rl.ID] = len(doc.Experience) - 1
	}
	roleRows.Close()
	if err := roleRows.Err(); err != nil {
		return nil, err
	}

	bulletRows, err := q.Query(ctx, `
		SELECT role_id, bullet_id, text, tags FROM web.resume_bullets
		WHERE sys_profile = $1 AND NOT retired ORDER BY sort_order, bullet_id`, profile)
	if err != nil {
		return nil, fmt.Errorf("query bullets: %w", err)
	}
	for bulletRows.Next() {
		var roleID, bulletID string
		var b DocBullet
		if err := bulletRows.Scan(&roleID, &bulletID, &b.Text, &b.Tags); err != nil {
			bulletRows.Close()
			return nil, err
		}
		if idx, ok := roleIdx[roleID]; ok {
			doc.Experience[idx].Bullets[bulletID] = b
		}
	}
	bulletRows.Close()
	if err := bulletRows.Err(); err != nil {
		return nil, err
	}

	eduRows, err := q.Query(ctx, `
		SELECT degree, institution, location FROM web.resume_education
		WHERE sys_profile = $1 ORDER BY sort_order, education_id`, profile)
	if err != nil {
		return nil, fmt.Errorf("query education: %w", err)
	}
	for eduRows.Next() {
		var e DocEducation
		if err := eduRows.Scan(&e.Degree, &e.Institution, &e.Location); err != nil {
			eduRows.Close()
			return nil, err
		}
		doc.Education = append(doc.Education, e)
	}
	eduRows.Close()
	return doc, eduRows.Err()
}
