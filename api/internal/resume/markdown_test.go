package resume

import (
	"strings"
	"testing"
)

func TestToMarkdown(t *testing.T) {
	doc := &Document{
		Contact: DocContact{Name: "Jane Doe", Email: "jane@example.com", Github: "github.com/jane"},
		Summary: "Backend engineer.",
		Skills: []DocSkill{
			{Text: "Go", Category: "Languages"},
			{Text: "Python", Category: "Languages"},
			{Text: "Postgres", Category: "Data"},
		},
		Education: []DocEducation{
			{Degree: "BSc Computer Science", Institution: "Some University"},
		},
	}
	bullets := []Bullet{
		{RoleID: "r1", RoleTitle: "Engineer", RoleCompany: "Acme", RoleDates: "2020–2024", BulletID: "b1", Text: "Built things"},
		{RoleID: "r1", RoleTitle: "Engineer", RoleCompany: "Acme", RoleDates: "2020–2024", BulletID: "b2", Text: "Shipped more"},
		{RoleID: "r2", RoleTitle: "Intern", RoleCompany: "Beta", RoleDates: "2019", BulletID: "b1", Text: "Learned"},
	}

	md := ToMarkdown(doc, bullets)

	for _, want := range []string{
		"# Jane Doe",
		"jane@example.com · github.com/jane",
		"## Summary",
		"Backend engineer.",
		"## Skills",
		"- **Languages:** Go, Python",
		"- **Data:** Postgres",
		"## Experience",
		"### Engineer — Acme",
		"*2020–2024*",
		"- Built things",
		"- Shipped more",
		"### Intern — Beta",
		"- Learned",
		"## Education",
		"- **BSc Computer Science** — Some University",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, md)
		}
	}

	// Deterministic: two renders are byte-identical (no map-order flakiness).
	if md != ToMarkdown(doc, bullets) {
		t.Error("ToMarkdown is not deterministic")
	}

	// Second role's bullets must follow its header, not the first role's.
	if i, j := strings.Index(md, "### Intern"), strings.Index(md, "- Learned"); i < 0 || j < 0 || j < i {
		t.Errorf("expected '- Learned' after '### Intern' header; got\n%s", md)
	}
}
