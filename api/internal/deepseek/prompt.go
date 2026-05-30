package deepseek

import (
	"strings"

	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

// systemPrompt frames the task. Kept short and focused — the schema lives
// in the user message where it's adjacent to the data.
//
// The model recommends REMOVALS only: the resume render starts from the full
// bullet pool (everything kept), and the LLM output is a thin diff of what to
// drop. This keeps the model's job conservative and its output small.
const systemPrompt = `You help tailor a resume to a specific job posting.

The bullets below are the candidate's full resume. Identify ONLY the bullets that should be REMOVED for this job — ones that add no signal for this specific role. Every bullet you do not list is kept by default. Be conservative: remove a bullet only when it is clearly off-topic or redundant for this job.

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
Return ONLY this JSON, listing ONLY the bullets to remove:
{
  "removals": [
    {"role_id": "<role>", "bullet_id": "<bullet>", "reason": "<one sentence>"}
  ]
}

Rules:
- List ONLY bullets to remove. Every bullet you omit is kept.
- "reason" must be one short sentence (under 25 words) on why the bullet is irrelevant or redundant for this job.
- Remove only clearly off-topic or redundant bullets. When in doubt, keep it (omit it).
- If nothing should be removed, return {"removals": []}.
`)
	return b.String()
}
