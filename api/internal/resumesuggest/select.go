package resumesuggest

import "sort"

// DefaultThreshold is the relevance gate, calibrated from the 2026-06 score
// distribution over the user's applied jobs: scores cluster high (median ~80)
// with a natural valley at ~50–59 separating "tangential" from "relevant", so
// 55 is the data-driven "is this bullet even on-topic for this job" bar.
const DefaultThreshold = 55

// DefaultImportantThreshold is the "too strong to cut" bar. A role with more
// bullets at/above this score than its cap expands past the cap (up to MaxCap)
// to keep them, rather than evicting an on-target bullet just to hit the cap.
// Set above the relevance gate but within the high-scoring cluster (the 2026-06
// distribution put the bulk of relevant bullets at 80+; 75 catches the strong
// ones a tight older-role cap would otherwise drop, like a must-have skill that
// only appears in a decade-old role).
const DefaultImportantThreshold = 75

// ScoredBullet is one bullet's relevance score within a role.
type ScoredBullet struct {
	BulletID string
	Score    int
}

// RoleScores is one role's bullets with scores, in résumé (canonical) order —
// most recent role first when the slice is built in résumé order.
type RoleScores struct {
	RoleID  string
	Bullets []ScoredBullet
}

// Selection is the keep/cut decision over the scored pool. Kept and Removed are
// composite "role_id.bullet_id" IDs in canonical order, the same form used in
// the LLM prompt and in jobs_resume.kept_bullet_ids.
type Selection struct {
	Kept    []string
	Removed []string
	// ScoreOf maps composite ID → score, for building removal reasons / audit.
	ScoreOf map[string]int
}

// IsKept reports whether the composite "role.bullet" survived selection.
func (s Selection) IsKept(compositeID string) bool {
	for _, k := range s.Kept {
		if k == compositeID {
			return true
		}
	}
	return false
}

// Select applies the calibrated keep-rule per role:
//
//	base      = clamp(count(score ≥ threshold), floor, cap)   // the soft target
//	important = count(score ≥ importantThreshold)             // too strong to cut
//	keptCount = clamp(important, base, maxCap)                // expand when needed
//
// then keeps the keptCount highest-scored bullets (ties broken by canonical
// order). So a role always keeps at least its floor, normally lands inside
// [floor, cap], but expands past the cap up to MaxCap when it holds more
// genuinely strong, on-target bullets than the cap allows — keeping an important
// point instead of evicting it to satisfy the cap. Roles are read in order
// (index 0 = most recent) for the tapered limits.
func Select(roles []RoleScores, limits Limits, threshold, importantThreshold int) Selection {
	sel := Selection{ScoreOf: map[string]int{}}
	for i, role := range roles {
		cap, floor, maxCap := limits.CapFor(i), limits.FloorFor(i), limits.MaxCapFor(i)

		// rank this role's bullets by score desc, stable on canonical order so
		// ties keep the more-recent/earlier-listed bullet.
		ranked := make([]ScoredBullet, len(role.Bullets))
		copy(ranked, role.Bullets)
		sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].Score > ranked[b].Score })

		passing, important := 0, 0
		for _, b := range role.Bullets {
			if b.Score >= threshold {
				passing++
			}
			if b.Score >= importantThreshold {
				important++
			}
		}
		base := clampInt(passing, floor, cap)
		keptCount := clampInt(important, base, maxCap)
		if keptCount > len(ranked) {
			keptCount = len(ranked)
		}

		keep := make(map[string]bool, keptCount)
		for _, b := range ranked[:keptCount] {
			keep[b.BulletID] = true
		}
		// emit kept/removed in canonical order
		for _, b := range role.Bullets {
			id := role.RoleID + "." + b.BulletID
			sel.ScoreOf[id] = b.Score
			if keep[b.BulletID] {
				sel.Kept = append(sel.Kept, id)
			} else {
				sel.Removed = append(sel.Removed, id)
			}
		}
	}
	return sel
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
