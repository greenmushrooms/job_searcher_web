package handlers

import (
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
