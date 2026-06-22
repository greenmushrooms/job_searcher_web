package resumesuggest

import "testing"

// the live canonical pool as of 2026-06: Lead/Present down to the oldest analyst
// role, in résumé (most-recent-first) order.
var liveRoles = []RoleBullets{
	{RoleID: "mlse_lead", Title: "Lead Data Engineer", Company: "MLSE", Count: 14},
	{RoleID: "mlse_de", Title: "Data Engineer", Company: "MLSE", Count: 9},
	{RoleID: "fourfinance", Title: "Data Engineer", Company: "4 Finance", Count: 6},
	{RoleID: "harris", Title: "Data Conversion Specialist", Company: "Harris", Count: 3},
	{RoleID: "cashmoney", Title: "Data Analyst", Company: "Cash Money", Count: 5},
}

func TestAnalyze_LiveResumeIsTooDense(t *testing.T) {
	rep := Analyze(liveRoles, DefaultLimits)

	if !rep.HasFindings() {
		t.Fatal("expected the live résumé to be flagged as too dense")
	}
	// Caps 6/5/4/3/3 vs counts 14/9/6/3/5 → over by 8/4/2/0/2 = 16.
	if rep.TrimTotal != 16 {
		t.Errorf("TrimTotal = %d, want 16", rep.TrimTotal)
	}
	if rep.Total != 37 {
		t.Errorf("Total = %d, want 37", rep.Total)
	}
	// 37 total vs a 20-bullet budget.
	if rep.OverBudget != 17 {
		t.Errorf("OverBudget = %d, want 17", rep.OverBudget)
	}

	lead := rep.Roles[0]
	if !lead.OverCap() || lead.Over != 8 || lead.Cap != 6 {
		t.Errorf("lead role: over=%v Over=%d Cap=%d, want over with Over=8 Cap=6", lead.OverCap(), lead.Over, lead.Cap)
	}
	if lead.Message == "" {
		t.Error("lead role should carry a suggestion message")
	}

	// The 3-bullet Harris role sits at its cap and must not be flagged.
	harris := rep.Roles[3]
	if harris.OverCap() || harris.Message != "" {
		t.Errorf("Harris (3 bullets, cap 3) should not be flagged, got Over=%d msg=%q", harris.Over, harris.Message)
	}
}

func TestAnalyze_CleanResumePasses(t *testing.T) {
	clean := []RoleBullets{
		{RoleID: "a", Count: 6},
		{RoleID: "b", Count: 5},
		{RoleID: "c", Count: 3},
		{RoleID: "d", Count: 2},
	}
	rep := Analyze(clean, DefaultLimits)
	if rep.HasFindings() {
		t.Errorf("clean résumé should not be flagged: %+v", rep)
	}
	if rep.TrimTotal != 0 || rep.OverBudget != 0 {
		t.Errorf("TrimTotal=%d OverBudget=%d, want 0/0", rep.TrimTotal, rep.OverBudget)
	}
	if rep.Verdict != "Bullet density looks good." {
		t.Errorf("Verdict = %q", rep.Verdict)
	}
}

func TestAnalyze_OverBudgetButPerRoleFine(t *testing.T) {
	// Six roles each exactly at the (tail) cap of 3 → 6+5+4+3+3+3 caps, but give
	// each a within-cap count that still sums past the 20 budget.
	roles := []RoleBullets{
		{RoleID: "a", Count: 6}, // cap 6
		{RoleID: "b", Count: 5}, // cap 5
		{RoleID: "c", Count: 4}, // cap 4
		{RoleID: "d", Count: 3}, // cap 3
		{RoleID: "e", Count: 3}, // cap 3
		{RoleID: "f", Count: 3}, // cap 3
	}
	rep := Analyze(roles, DefaultLimits)
	if rep.TrimTotal != 0 {
		t.Errorf("no role should be over cap, TrimTotal=%d", rep.TrimTotal)
	}
	if rep.Total != 24 || rep.OverBudget != 4 {
		t.Errorf("Total=%d OverBudget=%d, want 24/4", rep.Total, rep.OverBudget)
	}
	if !rep.HasFindings() {
		t.Error("over-budget résumé should still surface a finding")
	}
}

func TestAnalyze_ThinRoleUnderFloor(t *testing.T) {
	// A second role with a single bullet is below its floor (3) and should be
	// flagged as thin, not over-cap.
	roles := []RoleBullets{
		{RoleID: "a", Count: 5}, // cap 6, floor 4 — fine
		{RoleID: "b", Count: 1}, // cap 5, floor 3 — thin by 2
	}
	rep := Analyze(roles, DefaultLimits)
	if rep.TrimTotal != 0 {
		t.Errorf("no role is over cap, TrimTotal=%d", rep.TrimTotal)
	}
	if rep.PadTotal != 2 {
		t.Errorf("PadTotal = %d, want 2", rep.PadTotal)
	}
	thin := rep.Roles[1]
	if !thin.UnderFloor() || thin.Under != 2 || thin.Floor != 3 {
		t.Errorf("role b: under=%v Under=%d Floor=%d, want thin by 2 with floor 3", thin.UnderFloor(), thin.Under, thin.Floor)
	}
	if thin.OverCap() {
		t.Error("thin role must not also report over-cap")
	}
	if !rep.HasFindings() {
		t.Error("a thin role should surface a finding")
	}
}

func TestLimits_FloorTaperAndDefaults(t *testing.T) {
	// Floors taper 4/3/2/2 and hold the last value past the slice.
	got := []int{
		DefaultLimits.FloorFor(0), DefaultLimits.FloorFor(1),
		DefaultLimits.FloorFor(2), DefaultLimits.FloorFor(3), DefaultLimits.FloorFor(9),
	}
	want := []int{4, 3, 2, 2, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("FloorFor(%d) = %d, want %d", i, got[i], want[i])
		}
	}
	// An empty Limits falls back to the package defaults rather than panicking.
	var empty Limits
	if empty.FloorFor(0) != 4 || empty.CapFor(0) != 6 {
		t.Errorf("empty Limits floor/cap = %d/%d, want 4/6", empty.FloorFor(0), empty.CapFor(0))
	}
}

func TestAnalyze_TaperAppliesToLongTail(t *testing.T) {
	// A 6th role (index 5) should use the last cap (3), not panic past the slice.
	roles := make([]RoleBullets, 6)
	for i := range roles {
		roles[i] = RoleBullets{RoleID: "r", Count: 1}
	}
	rep := Analyze(roles, DefaultLimits)
	if got := rep.Roles[5].Cap; got != 3 {
		t.Errorf("role[5].Cap = %d, want 3 (last cap held)", got)
	}
}

func TestAnalyze_PositionPhrasing(t *testing.T) {
	roles := []RoleBullets{
		{RoleID: "recent", Count: 20},
		{RoleID: "mid", Count: 20},
		{RoleID: "old", Count: 20},
	}
	rep := Analyze(roles, DefaultLimits)
	if !contains(rep.Roles[0].Message, "most recent role") {
		t.Errorf("role[0] message = %q, want 'most recent role'", rep.Roles[0].Message)
	}
	if !contains(rep.Roles[2].Message, "oldest role") {
		t.Errorf("role[2] message = %q, want 'oldest role'", rep.Roles[2].Message)
	}
}

func TestAnalyze_Empty(t *testing.T) {
	rep := Analyze(nil, DefaultLimits)
	if rep.HasFindings() {
		t.Error("empty résumé should have no findings")
	}
	if rep.Verdict != "Bullet density looks good." {
		t.Errorf("Verdict = %q", rep.Verdict)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
