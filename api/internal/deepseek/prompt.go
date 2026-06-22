package deepseek

import (
	"fmt"
	"strings"

	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resumesuggest"
)

// systemPrompt frames the task. Kept short and focused — the schema lives
// in the user message where it's adjacent to the data.
//
// The model recommends REMOVALS only: the resume render starts from the full
// bullet pool (everything kept), and the LLM output is a thin diff of what to
// drop. This keeps the model's job conservative and its output small.
const systemPrompt = `You help tailor a resume to a specific job posting.

The bullets below are the candidate's full resume. Do three things:
1. REMOVALS — each role carries a bullet budget shown in its header as [N now — keep MIN–MAX]. For any role above its MAX, remove its weakest, least job-relevant bullets until it fits — keep the strongest and the ones matching THIS posting. Never reduce a role below its MIN floor, and don't trim a role already within its budget. Every bullet you do not list is kept.
2. REWRITES — for bullets worth keeping that would land harder if reworded for THIS job, give an improved version. Write it as a tight resume bullet, NOT a sentence: start with a strong past-tense action verb, no first person ("I"/"my"), no filler, ideally one line and under ~20 words. Keep the real achievement and any genuine metrics; never invent facts or numbers. Each rewrite must earn its place by matching a SPECIFIC requirement in the posting — prioritize bullets that already contain the job's must-have skills or tools and bring those to the front. Reword tense or phrasing only as a side effect, never as the reason to rewrite. Favor a few sharp, well-targeted rewrites over many cosmetic ones; quantity is not the goal, alignment is.
3. EDUCATION PRUNING — the EDUCATION list (if present) may include supplementary entries: professional exams, certifications, or standalone courses. Drop one only when it is clearly irrelevant to this posting. Degrees and diplomas are ALWAYS kept — never list them. Be conservative: when in doubt, keep.

Judging relevance:
- Judge each bullet by the transferable competency it demonstrates, not its surface domain. A bullet about building data products for marketing campaigns is evidence of end-to-end product ownership, not "marketing experience".
- Awards, recognition, and honors are keep-by-default: they signal credibility regardless of topic. Remove one only if space-critical and clearly weaker than everything kept.
- The per-role budgets already taper by recency: recent senior roles carry the highest MAX, the oldest and most junior roles the lowest. Trim each role to fit its own budget; when a role is over MAX, cut its weakest bullets first and keep cutting consistently across the early roles. Respect every MIN — a kept role should never look anemic.

When the job's tech stack differs from the candidate's (e.g. the posting wants GCP and the resume shows AWS), prefer REWRITES that foreground the transferable concept — streaming, warehousing, orchestration, scale — and any genuinely shared tools. Use ONLY tools and technologies that already appear in the candidate's bullet text; never introduce a tool the bullet does not mention, and never swap in a technology the candidate has not actually used. When the posting names a tool the candidate's bullets lack, lead with the transferable concept and the candidate's real tools — do not substitute the missing tool in.

Do not both remove and rewrite the same bullet. You return ONLY valid JSON in the exact schema specified by the user. No prose, no markdown, no commentary.`

// buildPrompt assembles the user message: job description, then the bullet
// pool grouped by role for readability. The response schema is restated
// inline so the model has it adjacent to the data — and so a prompt-template
// change requires no code change in the parser.
func buildPrompt(jobDescription string, bullets []resume.Bullet, education []resume.DocEducation) string {
	var b strings.Builder

	b.WriteString("=== JOB DESCRIPTION ===\n")
	b.WriteString(strings.TrimSpace(jobDescription))
	b.WriteString("\n\n=== RESUME BULLETS ===\n")
	b.WriteString("Each role header shows its bullet budget as [N now — keep MIN–MAX]: " +
		"trim a role over MAX down to its strongest, and never drop it below MIN.\n")

	// Per-role bullet budgets (min keep / max cap), tapered by recency — the same
	// limits the density check surfaces in the UI, so the prompt and the banner
	// never disagree. Role position fixes the budget; FromBullets preserves order.
	roles := resumesuggest.FromBullets(bullets)
	budgetIdx := make(map[string]int, len(roles))
	for i, r := range roles {
		budgetIdx[r.RoleID] = i
	}
	limits := resumesuggest.DefaultLimits

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
			i := budgetIdx[bul.RoleID]
			fmt.Fprintf(&b, "  [%d now — keep %d–%d]", roles[i].Count, limits.FloorFor(i), limits.CapFor(i))
			b.WriteString("\n")
		}
		b.WriteString("  - ")
		b.WriteString(bul.BulletID)
		b.WriteString(": ")
		b.WriteString(bul.Text)
		b.WriteString("\n")
	}

	if len(education) > 0 {
		b.WriteString("\n=== EDUCATION ===\n")
		for _, e := range education {
			b.WriteString("  [")
			b.WriteString(e.ID)
			b.WriteString("] ")
			b.WriteString(e.Degree)
			if e.Institution != "" {
				b.WriteString(" — ")
				b.WriteString(e.Institution)
			}
			b.WriteString("\n")
		}
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
  ],
  "education_removals": [
    {"education_id": "<education>", "reason": "<one sentence>"}
  ]
}

Rules:
- removals: bring each role within the bullet budget in its header. Remove the weakest bullets from any role over its MAX; keep at least its MIN; leave roles already within budget alone (when in doubt, keep). Among bullets competing for the same slot, keep the ones matching this posting and cut generic filler. Awards/recognition are keep-by-default and count toward the budget. Judge by transferable competency, not surface domain.
- rewrites: bullets that read clearly better reworded for this job. Each new_text is a concise resume bullet — action-verb first, no first person, no trailing prose, under ~20 words. Preserve the real achievement and any genuine metrics; invent nothing — use only technologies already in the candidate's bullet, never add or swap one in. Bring must-have skills/tools the candidate genuinely has to the front. When the posting's stack differs, lead with the transferable concept (streaming, modeling, orchestration, scale), not the missing tool. Prefer a few high-signal rewrites over many; do not pad.
- education_removals: list ONLY supplementary education entries to drop for this job — professional exams, certifications, or standalone courses that are clearly irrelevant to the posting. Degrees and diplomas are keep-by-default: NEVER list them. When in doubt, keep. Leave empty if every entry is relevant, or if the only entries are degrees.
- Never both remove and rewrite the same bullet.
- "reason" must be one short sentence (under 25 words) that names the SPECIFIC posting requirement the change serves (e.g. "foregrounds PySpark, a must-have skill"). A reason about grammar or tense alone is not a valid rewrite.
- removals and education_removals may be empty when nothing should be cut. rewrites should surface the genuinely strong, job-matched opportunities — usually a few; leave them empty only when no bullet can be made more aligned without fabricating.
`)
	return b.String()
}
