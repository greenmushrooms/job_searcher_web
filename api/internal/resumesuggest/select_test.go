package resumesuggest

import "testing"

func bullets(scores ...int) []ScoredBullet {
	out := make([]ScoredBullet, len(scores))
	for i, s := range scores {
		out[i] = ScoredBullet{BulletID: string(rune('a' + i)), Score: s}
	}
	return out
}

// sel is a test helper that runs Select with the package default thresholds.
func sel(roles []RoleScores) Selection {
	return Select(roles, DefaultLimits, DefaultThreshold, DefaultImportantThreshold)
}

func TestSelect_StaysAtCapWhenNoBulletsAreImportant(t *testing.T) {
	// 8 bullets all relevant (≥55) but none important (<75) → cap (6) holds.
	roles := []RoleScores{{RoleID: "lead", Bullets: bullets(74, 70, 68, 66, 64, 62, 60, 58)}}
	if got := countRole(sel(roles).Kept, "lead"); got != 6 {
		t.Errorf("lead kept %d, want 6 (cap; no expansion without important bullets)", got)
	}
}

func TestSelect_ExpandsPastCapForImportantBullets(t *testing.T) {
	// de role (index 1): cap 5, maxCap 7. 6 bullets all ≥75 → expand to 6.
	roles := []RoleScores{
		{RoleID: "lead", Bullets: bullets(60)}, // filler recent role
		{RoleID: "de", Bullets: bullets(90, 88, 85, 82, 80, 78)},
	}
	if got := countRole(sel(roles).Kept, "de"); got != 6 {
		t.Errorf("de kept %d, want 6 (expanded past cap 5 to hold 6 important bullets)", got)
	}
}

func TestSelect_ExpansionStopsAtMaxCap(t *testing.T) {
	// lead (index 0): cap 6, maxCap 8. 10 important bullets → capped at 8.
	roles := []RoleScores{{RoleID: "lead", Bullets: bullets(99, 98, 97, 96, 95, 94, 93, 92, 91, 90)}}
	s := sel(roles)
	if got := countRole(s.Kept, "lead"); got != 8 {
		t.Errorf("lead kept %d, want 8 (maxCap ceiling)", got)
	}
	if len(s.Removed) != 2 {
		t.Errorf("removed %d, want 2 (10 - maxCap 8)", len(s.Removed))
	}
}

func TestSelect_FloorHoldsWhenNothingRelevant(t *testing.T) {
	// off-target old role at index 2 (cap 4, floor 2): nothing clears 55 → floor
	// keeps the 2 best; importance doesn't lower the floor.
	roles := []RoleScores{
		{RoleID: "a", Bullets: bullets(60)},
		{RoleID: "b", Bullets: bullets(60)},
		{RoleID: "old", Bullets: bullets(40, 35, 20, 15, 10)},
	}
	s := sel(roles)
	if got := countRole(s.Kept, "old"); got != 2 {
		t.Fatalf("old kept %d, want 2 (floor)", got)
	}
	if !s.IsKept("old.a") || !s.IsKept("old.b") || s.IsKept("old.c") {
		t.Errorf("old kept the wrong bullets: %v", s.Kept)
	}
}

func TestSelect_KeepsHighestScored(t *testing.T) {
	// de (index 1): cap 5, maxCap 7. Mixed scores; 4 important (≥75) but base is 5
	// (5 pass ≥55), so keptCount = clamp(4, 5, 7) = 5 → keep the top 5 by score.
	roles := []RoleScores{
		{RoleID: "lead", Bullets: bullets(60)},
		{RoleID: "de", Bullets: bullets(90, 40, 85, 60, 78, 58)},
	}
	s := sel(roles)
	if got := countRole(s.Kept, "de"); got != 5 {
		t.Fatalf("de kept %d, want 5", got)
	}
	if s.IsKept("de.b") { // 40 — lowest, the only cut
		t.Errorf("expected de.b (40) removed, kept=%v", s.Kept)
	}
}

func TestSelect_TieBreakIsCanonicalOrder(t *testing.T) {
	// 9 bullets tied at 80 (important), role index 0: cap 6, maxCap 8 → expand to
	// 8 and drop the last canonical bullet.
	roles := []RoleScores{{RoleID: "r", Bullets: bullets(80, 80, 80, 80, 80, 80, 80, 80, 80)}}
	s := sel(roles)
	if got := countRole(s.Kept, "r"); got != 8 {
		t.Fatalf("kept %d, want 8 (maxCap)", got)
	}
	if len(s.Removed) != 1 || s.Removed[0] != "r.i" {
		t.Errorf("tie should drop the last canonical bullet, removed=%v", s.Removed)
	}
}

func TestSelect_EmptyRole(t *testing.T) {
	s := sel([]RoleScores{{RoleID: "x", Bullets: nil}})
	if len(s.Kept) != 0 || len(s.Removed) != 0 {
		t.Errorf("empty role yields nothing, got kept=%v removed=%v", s.Kept, s.Removed)
	}
}

func countRole(ids []string, role string) int {
	n := 0
	for _, id := range ids {
		if len(id) > len(role) && id[:len(role)] == role && id[len(role)] == '.' {
			n++
		}
	}
	return n
}
