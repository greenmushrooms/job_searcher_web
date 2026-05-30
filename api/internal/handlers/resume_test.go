package handlers

import (
	"net/http"
	"net/url"
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

func TestBuildDraftView(t *testing.T) {
	res := &resume.Resume{
		Version: "v2-abcd1234",
		Bullets: []resume.Bullet{
			{RoleID: "r1", RoleTitle: "Eng", RoleCompany: "Acme", RoleDates: "2020", BulletID: "b1", Text: "kept one"},
			{RoleID: "r1", RoleTitle: "Eng", RoleCompany: "Acme", RoleDates: "2020", BulletID: "b2", Text: "dropped one"},
			{RoleID: "r2", RoleTitle: "Dev", RoleCompany: "Globex", RoleDates: "2018", BulletID: "b9", Text: "kept two"},
		},
	}
	p := &draftedEventPayload{
		Model:         "deepseek-v4-pro",
		ResumeVersion: res.Version,
		CostUSD:       0.01,
		Removals: []deepseek.Removal{
			{RoleID: "r1", BulletID: "b2", Reason: "redundant"},
			// Points at a bullet no longer present — must be ignored, not crash.
			{RoleID: "rX", BulletID: "bX", Reason: "stale"},
		},
	}

	view := buildDraftView("job-1", "Slava", "just now", p, res)

	if view.TotalCount != 3 {
		t.Errorf("TotalCount: got %d, want 3", view.TotalCount)
	}
	if view.KeptCount != 2 {
		t.Errorf("KeptCount: got %d, want 2", view.KeptCount)
	}
	if len(view.Roles) != 2 {
		t.Fatalf("Roles: got %d, want 2 (grouped by role)", len(view.Roles))
	}
	// Role order follows bullet order in the resume.
	if view.Roles[0].RoleID != "r1" || view.Roles[1].RoleID != "r2" {
		t.Errorf("role order: got %s,%s want r1,r2", view.Roles[0].RoleID, view.Roles[1].RoleID)
	}

	// b2 is the only removal that matches a live bullet.
	var b2 *draftBulletView
	for i := range view.Roles[0].Bullets {
		if view.Roles[0].Bullets[i].CompositeID == "r1.b2" {
			b2 = &view.Roles[0].Bullets[i]
		}
	}
	if b2 == nil {
		t.Fatal("r1.b2 not found in view")
	}
	if b2.Keep {
		t.Error("r1.b2 should be unchecked (Keep=false)")
	}
	if b2.Reason != "redundant" {
		t.Errorf("r1.b2 reason: got %q, want %q", b2.Reason, "redundant")
	}
}

func TestBuildDraftView_Rewrites(t *testing.T) {
	res := &resume.Resume{
		Bullets: []resume.Bullet{
			{RoleID: "r1", BulletID: "b1", Text: "original one"},
			{RoleID: "r1", BulletID: "b2", Text: "drop me"},
		},
	}
	p := &draftedEventPayload{
		Rewrites: []deepseek.Rewrite{
			{RoleID: "r1", BulletID: "b1", NewText: "punchier one", Reason: "matches stack"},
			// Rewrite on a removed bullet must be ignored.
			{RoleID: "r1", BulletID: "b2", NewText: "won't show", Reason: "x"},
		},
		Removals: []deepseek.Removal{{RoleID: "r1", BulletID: "b2", Reason: "off-topic"}},
	}

	view := buildDraftView("job", "Slava", "now", p, res)

	if view.RewriteCount != 1 {
		t.Errorf("RewriteCount: got %d, want 1 (removed bullet's rewrite ignored)", view.RewriteCount)
	}
	b1 := view.Roles[0].Bullets[0]
	if b1.NewText != "punchier one" || b1.RewriteReason != "matches stack" {
		t.Errorf("b1 rewrite: got %q/%q", b1.NewText, b1.RewriteReason)
	}
	b2 := view.Roles[0].Bullets[1]
	if b2.NewText != "" {
		t.Errorf("b2 (removed) should carry no rewrite, got %q", b2.NewText)
	}
}

func TestBuildFinalBullets(t *testing.T) {
	res := &resume.Resume{
		Bullets: []resume.Bullet{
			{RoleID: "r1", BulletID: "b1", Text: "canonical one"},
			{RoleID: "r1", BulletID: "b2", Text: "canonical two"},
			{RoleID: "r2", BulletID: "b9", Text: "canonical nine"},
		},
	}
	rewrites := map[string]string{"r1.b2": "ai two"}
	form := url.Values{
		"text_r1.b1": {"canonical one"}, // unchanged -> canonical
		"text_r1.b2": {"ai two"},        // matches rewrite -> ai
		"text_r2.b9": {"hand edited"},   // -> manual
	}
	r := &http.Request{Form: form}
	kept := []string{"r1.b1", "r1.b2", "r2.b9", "bad-no-dot"}

	got := buildFinalBullets(kept, r, res, rewrites)
	if len(got) != 3 {
		t.Fatalf("got %d bullets, want 3 (bad id skipped): %+v", len(got), got)
	}
	want := map[string]string{"r1.b1": "canonical", "r1.b2": "ai", "r2.b9": "manual"}
	for _, fb := range got {
		cid := fb.RoleID + "." + fb.BulletID
		if want[cid] != fb.Source {
			t.Errorf("%s: source got %q, want %q (text=%q)", cid, fb.Source, want[cid], fb.Text)
		}
	}
}

func TestBuildFinalBullets_EmptyFallsBackToCanonical(t *testing.T) {
	res := &resume.Resume{Bullets: []resume.Bullet{{RoleID: "r1", BulletID: "b1", Text: "canonical one"}}}
	r := &http.Request{Form: url.Values{"text_r1.b1": {"   "}}} // blank -> canonical fallback
	got := buildFinalBullets([]string{"r1.b1"}, r, res, nil)
	if len(got) != 1 || got[0].Text != "canonical one" || got[0].Source != "canonical" {
		t.Errorf("blank should fall back to canonical, got %+v", got)
	}
}
