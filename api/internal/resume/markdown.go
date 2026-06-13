package resume

import (
	"fmt"
	"strings"
)

// ToMarkdown renders a resume as a stable markdown document. The header,
// summary, skills and education come from the full Document; the Experience
// section is driven by the ordered bullet slice (a template subset, the full
// pool, or a tailored selection) grouped into roles in slice order.
//
// Experience deliberately comes from the ordered slice rather than Document's
// bullet map (which is keyed by bullet_id and therefore unordered): a markdown
// diff is only meaningful if both sides render in a deterministic order.
func ToMarkdown(doc *Document, bullets []Bullet) string {
	var b strings.Builder

	if doc != nil {
		if doc.Contact.Name != "" {
			fmt.Fprintf(&b, "# %s\n", doc.Contact.Name)
		}
		if contact := joinNonEmpty(" · ", doc.Contact.Email, doc.Contact.Phone, doc.Contact.Github, doc.Contact.Location); contact != "" {
			fmt.Fprintf(&b, "%s\n", contact)
		}
		if s := strings.TrimSpace(doc.Summary); s != "" {
			b.WriteString("\n## Summary\n\n")
			b.WriteString(s)
			b.WriteString("\n")
		}
		if md := skillsMarkdown(doc.Skills); md != "" {
			b.WriteString("\n## Skills\n\n")
			b.WriteString(md)
		}
	}

	if exp := experienceMarkdown(bullets); exp != "" {
		b.WriteString("\n## Experience\n\n")
		b.WriteString(exp)
	}

	if doc != nil && len(doc.Education) > 0 {
		b.WriteString("\n## Education\n\n")
		for _, e := range doc.Education {
			rest := joinNonEmpty(", ", e.Institution, e.Location)
			switch {
			case e.Degree != "" && rest != "":
				fmt.Fprintf(&b, "- **%s** — %s\n", e.Degree, rest)
			case e.Degree != "":
				fmt.Fprintf(&b, "- **%s**\n", e.Degree)
			case rest != "":
				fmt.Fprintf(&b, "- %s\n", rest)
			}
		}
	}

	return strings.TrimSpace(b.String()) + "\n"
}

// skillsMarkdown groups skills by category (preserving first-seen order) into
// one bullet per category: "- **Category:** a, b, c".
func skillsMarkdown(skills []DocSkill) string {
	if len(skills) == 0 {
		return ""
	}
	var order []string
	groups := map[string][]string{}
	for _, s := range skills {
		if _, seen := groups[s.Category]; !seen {
			order = append(order, s.Category)
		}
		groups[s.Category] = append(groups[s.Category], s.Text)
	}
	var b strings.Builder
	for _, cat := range order {
		items := strings.Join(groups[cat], ", ")
		if strings.TrimSpace(cat) != "" {
			fmt.Fprintf(&b, "- **%s:** %s\n", cat, items)
		} else {
			fmt.Fprintf(&b, "- %s\n", items)
		}
	}
	return b.String()
}

// experienceMarkdown groups the ordered bullets into role sections. Bullets
// arrive sorted by (role, bullet), so a role break is just a change in RoleID.
func experienceMarkdown(bullets []Bullet) string {
	var b strings.Builder
	curRole := ""
	started := false
	for _, bl := range bullets {
		if bl.RoleID != curRole {
			curRole = bl.RoleID
			if started {
				b.WriteString("\n") // blank line between role blocks
			}
			header := bl.RoleTitle
			if bl.RoleCompany != "" {
				if header != "" {
					header += " — " + bl.RoleCompany
				} else {
					header = bl.RoleCompany
				}
			}
			if header != "" {
				fmt.Fprintf(&b, "### %s\n\n", header)
			}
			if bl.RoleDates != "" {
				fmt.Fprintf(&b, "*%s*\n\n", bl.RoleDates)
			}
			started = true
		}
		fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(bl.Text))
	}
	return b.String()
}

func joinNonEmpty(sep string, parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, sep)
}
