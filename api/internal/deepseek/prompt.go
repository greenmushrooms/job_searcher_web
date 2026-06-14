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
2. REWRITES — for bullets worth keeping that would land harder if reworded for THIS job, give an improved version. Write it as a tight resume bullet, NOT a sentence: start with a strong past-tense action verb, no first person ("I"/"my"), no filler, ideally one line and under ~20 words. Keep the real achievement and any genuine metrics; never invent facts or numbers. Aim for at least two or three rewrites — almost every resume has several bullets whose wording could lead harder with this posting's language, so surface the strongest few rather than stopping at one. Reword only what is genuinely there; do not pad with fabrications to hit a count.

Judging relevance:
- Judge each bullet by the transferable competency it demonstrates, not its surface domain. A bullet about building data products for marketing campaigns is evidence of end-to-end product ownership, not "marketing experience".
- Awards, recognition, and honors are keep-by-default: they signal credibility regardless of topic. Remove one only if space-critical and clearly weaker than everything kept.
- Prune oldest and most junior roles the hardest — recent senior work earns its space; decade-old analyst tasks rarely do. Be consistent: if you cut weak bullets from one early role, cut comparable bullets from the others.

When the job's tech stack differs from the candidate's (e.g. the posting wants GCP and the resume shows AWS), prefer REWRITES that foreground the transferable concept — streaming, warehousing, orchestration, scale — and any genuinely shared tools. Never swap in technology the candidate has not actually used.

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
- removals: list ONLY bullets to remove. Every omitted bullet is kept. When in doubt, keep it. Awards/recognition are keep-by-default. Judge by transferable competency, not surface domain. Prune the oldest/most junior roles hardest, and consistently.
- rewrites: bullets that read clearly better reworded for this job. Each new_text is a concise resume bullet — action-verb first, no first person, no trailing prose, under ~20 words. Preserve the real achievement and any genuine metrics; invent nothing — never claim tools the candidate has not used. When the posting's stack differs from the candidate's, reword kept bullets to lead with the transferable concept (streaming, modeling, orchestration, scale) and shared tools. Return at least two or three of the strongest rewrites — do not stop at one when more bullets could be sharpened.
- Never both remove and rewrite the same bullet.
- "reason" must be one short sentence (under 25 words).
- removals may be empty when nothing should be cut. rewrites should normally carry two or three suggestions; leave them empty only when the resume already reads perfectly for this posting.
`)
	return b.String()
}
