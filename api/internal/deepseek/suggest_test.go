package deepseek

import "testing"

func TestSuggestModelDefault(t *testing.T) {
	t.Setenv("DEEPSEEK_SUGGEST_MODEL", "")
	if got := suggestModel(); got != "deepseek-v4-flash" {
		t.Errorf("suggestModel() = %q, want deepseek-v4-flash", got)
	}
	t.Setenv("DEEPSEEK_SUGGEST_MODEL", "deepseek-v4-pro")
	if got := suggestModel(); got != "deepseek-v4-pro" {
		t.Errorf("suggestModel() with override = %q, want deepseek-v4-pro", got)
	}
}
