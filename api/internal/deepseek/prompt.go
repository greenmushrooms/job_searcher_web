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

The bullets below are the candidate's full resume. Do two things:
1. REMOVALS — list bullets that should be removed for this job (clearly off-topic or redundant). Every bullet you do not list is kept by default. Be conservative.
2. REWRITES — for bullets worth keeping that would land harder if reworded for THIS job, give an improved version. Keep the real achievement and any genuine metrics; never invent facts or numbers. Only rewrite when it clearly helps — most bullets need none.

Do not both remove and rewrite the same bullet. You return ONLY valid JSON in the exact schema specified by the user. No prose, no markdown, no commentary.`

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
Return ONLY this JSON:
{
  "removals": [
    {"role_id": "<role>", "bullet_id": "<bullet>", "reason": "<one sentence>"}
  ],
  "rewrites": [
    {"role_id": "<role>", "bullet_id": "<bullet>", "new_text": "<improved bullet>", "reason": "<one sentence>"}
  ]
}

Rules:
- removals: list ONLY bullets to remove. Every omitted bullet is kept. When in doubt, keep it.
- rewrites: only bullets that read clearly better reworded for this job. Preserve the real achievement and any genuine metrics; invent nothing. Most bullets should be omitted (kept as-is).
- Never both remove and rewrite the same bullet.
- "reason" must be one short sentence (under 25 words).
- If nothing applies, return empty arrays: {"removals": [], "rewrites": []}.
`)
	return b.String()
}
