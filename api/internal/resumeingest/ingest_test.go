package resumeingest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseRejectsNonResumes(t *testing.T) {
	cases := []struct {
		name, in, wantErr string
	}{
		{"no experience", `{"experience":[]}`, "no work experience"},
		{"role without identity", `{"experience":[{"title":"","company":"","bullets":[{"text":"x"}]}]}`, "missing both title and company"},
		{"no bullets anywhere", `{"experience":[{"title":"Analyst","company":"Acme","bullets":[]}]}`, "no bullet points"},
	}
	for _, c := range cases {
		if _, err := Parse(json.RawMessage(c.in)); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: got err %v, want containing %q", c.name, err, c.wantErr)
		}
	}
}

func TestParseAcceptsMinimalResume(t *testing.T) {
	s, err := Parse(json.RawMessage(`{
		"contact":{"name":"Jane Doe"},
		"experience":[{"title":"Analyst","company":"Acme Corp","bullets":[{"text":"Did things"}]}]
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Contact.Name != "Jane Doe" || len(s.Experience) != 1 {
		t.Fatalf("unexpected parse result: %+v", s)
	}
}

func TestUniqueSlug(t *testing.T) {
	seen := map[string]bool{}
	got := []string{
		uniqueSlug("Acme Corp", "", 0, seen),
		uniqueSlug("Acme Corp", "", 1, seen),         // dupe company → positional suffix
		uniqueSlug("", "Senior Analyst", 2, seen),    // falls back to title
		uniqueSlug("", "", 3, seen),                  // falls back to "role"
		uniqueSlug("Ünïcode & Co.!!", "", 4, seen),   // non-alnum squashed
	}
	want := []string{"acme_corp", "acme_corp_2", "senior_analyst", "role", "n_code_co"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("slug %d: got %q want %q", i, got[i], want[i])
		}
	}
}
