package deepseek

import (
	"strings"

	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

// systemPrompt frames the task. Kept short and focused — the schema lives
// in the user message where it's adjacent to the data.
const systemPrompt = `You help tailor a resume to a specific job posting.

For each resume bullet, decide whether to KEEP it (relevant to the job) or DROP it (not relevant). Keep bullets that demonstrate skills, achievements, or experience the job actually asks for. Drop bullets that don't add signal for this specific role.

You return ONLY valid JSON in the exact schema specified by the user. No prose, no markdown, no commentary.`

// buildPrompt assembles the user message: job description, then the bullet
// pool grouped by role for readability. The response schema is restated
// inline so the model has it adjacent to the data — and so a prompt-template
// change requires no code change in the parser.
func buildPrompt(jobDescription string, bullets []resume.Bullet) string {
	var b strings.Builder

	b.WriteString("=== JOB DESCRIPTION ===\n")
	b.WriteString(strings.TrimSpace(jobDescription))
	b.WriteString("\n\n=== RESUME BULLETS ===\n")

	// Group by role in input order — resume.Load already returns bullets in
	// file order, with bullet IDs sorted within each role for determinism.
	var currentRole string
	for _, bul := range bullets {
		if bul.RoleID != currentRole {
			if currentRole != "" {
				b.WriteString("\n")
			}
			currentRole = bul.RoleID
			b.WriteString("[")
			b.WriteString(bul.RoleID)
			b.WriteString("] ")
			b.WriteString(bul.RoleTitle)
			b.WriteString(" — ")
			b.WriteString(bul.RoleCompany)
			if bul.RoleDates != "" {
				b.WriteString(" (")
				b.WriteString(bul.RoleDates)
				b.WriteString(")")
			}
			b.WriteString("\n")
		}
		b.WriteString("  - ")
		b.WriteString(bul.BulletID)
		b.WriteString(": ")
		b.WriteString(bul.Text)
		b.WriteString("\n")
	}

	b.WriteString(`
=== RESPONSE SCHEMA ===
Return ONLY this JSON, with one decision per bullet above:
{
  "decisions": [
    {"role_id": "<role>", "bullet_id": "<bullet>", "keep": true | false, "reason": "<one sentence>"}
  ]
}

Rules:
- Include every bullet from above, in any order.
- "reason" must be one short sentence (under 25 words) tying the bullet to the job description.
- Default to KEEP when uncertain — the user will trim further. Drop only when clearly off-topic.
`)
	return b.String()
}
