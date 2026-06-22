package deepseek

import (
	"strings"
	"testing"
)

func TestScorerPrompt_IsCalibratedAClean(t *testing.T) {
	if ScorerVersion != "v10" {
		t.Errorf("ScorerVersion = %q, want v10", ScorerVersion)
	}
	// The winning rubric: 0–100 scale, transferable-competency framing, anchors.
	for _, want := range []string{
		"0–100 scale",
		"transfers to this role",
		"not by surface keyword overlap or domain",
		`{"score": <int 0-100>}`,
	} {
		if !strings.Contains(scorerSystemPrompt, want) {
			t.Errorf("scorer system prompt missing %q", want)
		}
	}
	// The leaked marketing example from A_anchored must NOT be present.
	if strings.Contains(scorerSystemPrompt, "marketing campaign") {
		t.Error("scorer prompt still carries the leaked example — should be A_clean")
	}
}

func TestScorerUserPrompt_FramesPostingAndBullet(t *testing.T) {
	p := scorerUserPrompt("Senior Data Engineer\nWe want dbt and Snowflake.", "Pioneered DBT adoption.")
	for _, want := range []string{"=== JOB POSTING ===", "=== RÉSUMÉ BULLET ===", "Pioneered DBT adoption.", "Score this bullet's relevance"} {
		if !strings.Contains(p, want) {
			t.Errorf("user prompt missing %q\n%s", want, p)
		}
	}
}

func TestScorerModel_DefaultsToFlash(t *testing.T) {
	t.Setenv("DEEPSEEK_SCORER_MODEL", "")
	if got := scorerModel(); got != "deepseek-v4-flash" {
		t.Errorf("scorerModel() = %q, want deepseek-v4-flash", got)
	}
	t.Setenv("DEEPSEEK_SCORER_MODEL", "deepseek-v4-pro")
	if got := scorerModel(); got != "deepseek-v4-pro" {
		t.Errorf("scorerModel() override = %q, want deepseek-v4-pro", got)
	}
}
