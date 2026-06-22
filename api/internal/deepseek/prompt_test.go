package deepseek

import (
	"strings"
	"testing"

	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

// makeBullets builds n bullets under one role, matching the shape resume.Load
// returns (role metadata repeated on every bullet of the role).
func makeBullets(roleID, title, company string, n int) []resume.Bullet {
	out := make([]resume.Bullet, n)
	for i := range out {
		out[i] = resume.Bullet{
			RoleID:      roleID,
			RoleTitle:   title,
			RoleCompany: company,
			BulletID:    string(rune('a' + i)),
			Text:        "did a thing",
		}
	}
	return out
}

func TestBuildPrompt_EmbedsPerRoleBudget(t *testing.T) {
	var bullets []resume.Bullet
	bullets = append(bullets, makeBullets("lead", "Lead Data Engineer", "MLSE", 14)...)
	bullets = append(bullets, makeBullets("de", "Data Engineer", "MLSE", 9)...)
	bullets = append(bullets, makeBullets("old", "Data Analyst", "Cash Money", 5)...)

	got := buildPrompt("Senior Data Engineer wanted.", bullets, nil)

	// Recency-tapered budgets: role 0 caps at 6 (floor 4), role 1 at 5 (floor 3),
	// role 2 at 4 (floor 2). The header must carry the live count and the band.
	for _, want := range []string{
		"[14 now — keep 4–6]",
		"[9 now — keep 3–5]",
		"[5 now — keep 2–4]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing role budget %q\n---\n%s", want, got)
		}
	}

	// The legend explaining the annotation must be present so the model can read it.
	if !strings.Contains(got, "keep MIN–MAX") {
		t.Error("prompt missing the budget legend")
	}
}

func TestSystemPrompt_DrivesRemovalsFromBudget(t *testing.T) {
	if !strings.Contains(systemPrompt, "bullet budget") {
		t.Error("system prompt no longer references the per-role bullet budget")
	}
	if strings.Contains(systemPrompt, "Every bullet you do not list is kept by default. Be conservative.") {
		t.Error("system prompt still uses the old blanket-conservative removal rule")
	}
}
