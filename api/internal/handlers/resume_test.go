package handlers

import (
	"strings"
	"testing"

	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

func TestEffectiveRemovals(t *testing.T) {
	rm := deepseek.Removal{RoleID: "r1", BulletID: "b1", Reason: "off-topic"}

	t.Run("removals present passthrough", func(t *testing.T) {
		p := &draftedEventPayload{Removals: []deepseek.Removal{rm}}
		got := p.effectiveRemovals()
		if len(got) != 1 || got[0] != rm {
			t.Fatalf("got %v, want [%v]", got, rm)
		}
	})

	t.Run("legacy decisions folded to removals", func(t *testing.T) {
		p := &draftedEventPayload{LegacyDecisions: []legacyDecision{
			{RoleID: "r1", BulletID: "b1", Keep: false, Reason: "drop me"},
			{RoleID: "r1", BulletID: "b2", Keep: true, Reason: "keep me"},
		}}
		got := p.effectiveRemovals()
		if len(got) != 1 {
			t.Fatalf("got %d removals, want 1: %v", len(got), got)
		}
		if got[0].BulletID != "b1" || got[0].Reason != "drop me" {
			t.Errorf("unexpected removal: %+v", got[0])
		}
	})

	t.Run("empty payload yields nothing", func(t *testing.T) {
		p := &draftedEventPayload{}
		if got := p.effectiveRemovals(); len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})

	t.Run("removals win over legacy when both set", func(t *testing.T) {
		p := &draftedEventPayload{
			Removals:        []deepseek.Removal{rm},
			LegacyDecisions: []legacyDecision{{RoleID: "rX", BulletID: "bX", Keep: false}},
		}
		got := p.effectiveRemovals()
		if len(got) != 1 || got[0] != rm {
			t.Errorf("got %v, want [%v]", got, rm)
		}
	})
}

func TestTailoredBullets(t *testing.T) {
	base := []resume.Bullet{
		{RoleID: "r1", BulletID: "b1", Text: "keep me"},
		{RoleID: "r1", BulletID: "b2", Text: "rewrite me"},
		{RoleID: "r1", BulletID: "b3", Text: "drop me"},
		{RoleID: "r2", BulletID: "b9", Text: "second role"},
	}
	removed := map[string]string{"r1.b3": "off-topic"}
	rewritten := map[string]deepseek.Rewrite{
		"r1.b2": {RoleID: "r1", BulletID: "b2", NewText: "rewritten text"},
		// Points at a removed bullet — applied or not, the bullet is gone anyway.
		"r1.b3": {RoleID: "r1", BulletID: "b3", NewText: "irrelevant"},
	}

	got := tailoredBullets(base, removed, rewritten)

	if len(got) != 3 {
		t.Fatalf("got %d bullets, want 3 (one dropped): %+v", len(got), got)
	}
	// Order preserved, b3 dropped, b2 rewritten.
	if got[0].BulletID != "b1" || got[1].BulletID != "b2" || got[2].BulletID != "b9" {
		t.Errorf("order/selection wrong: %+v", got)
	}
	if got[1].Text != "rewritten text" {
		t.Errorf("b2 not rewritten: %q", got[1].Text)
	}
}

func TestDiffMarkdownHTML(t *testing.T) {
	base := "# Resume\n\n- alpha\n- beta\n"
	tailored := "# Resume\n\n- alpha\n- BETA improved\n"

	html := string(diffMarkdownHTML(base, tailored))

	if !strings.Contains(html, "diff-del") || !strings.Contains(html, "beta") {
		t.Errorf("expected a deletion line for 'beta':\n%s", html)
	}
	if !strings.Contains(html, "diff-add") || !strings.Contains(html, "BETA improved") {
		t.Errorf("expected an addition line for 'BETA improved':\n%s", html)
	}
	if !strings.Contains(html, "diff-ctx") {
		t.Errorf("expected context lines for the unchanged header:\n%s", html)
	}
}

func TestNameFromMarkdown(t *testing.T) {
	if got := nameFromMarkdown("# Jane Doe\njane@example.com\n"); got != "Jane Doe" {
		t.Errorf("got %q, want %q", got, "Jane Doe")
	}
	if got := nameFromMarkdown("no heading here\n"); got != "resume" {
		t.Errorf("got %q, want fallback 'resume'", got)
	}
}
