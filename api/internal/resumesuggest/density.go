// Package resumesuggest produces deterministic, no-LLM suggestions about a
// résumé's shape. The first check is bullet density: how many bullets sit under
// each role versus a sane per-role cap, and whether the whole experience
// section blows past a clean page budget.
//
// This is the "too many points per job" judgement made mechanical — the same
// call a human makes skimming a résumé, but cheap, stable, and unit-testable.
// It runs off the structured canonical bullet pool (web.resume_bullets, already
// grouped by role in résumé order), so it needs no model round-trip and costs
// nothing. The DeepSeek tailoring pass is deliberately conservative about
// removals; this pipe is the counterweight that flags when the source résumé is
// simply carrying too much.
package resumesuggest

import "fmt"

// RoleBullets is one role and its bullet count, in résumé order (most recent
// first — role sort_order 0 is the current/most senior job). Callers fold the
// flat (role, bullet) bullet pool into this before analysis.
type RoleBullets struct {
	RoleID  string
	Title   string
	Company string
	Dates   string
	Count   int
}

// Limits tunes the density heuristic. Caps is the per-role bullet ceiling and
// Floors the per-role minimum to keep, both indexed by role position (0 = most
// recent); roles past either slice use its last entry. Budget is the
// recommended ceiling on total experience bullets across all roles — the
// page-fit guard that catches a résumé that stays under every per-role cap yet
// still runs long.
type Limits struct {
	Caps    []int
	Floors  []int
	MaxCaps []int
	Budget  int
}

// DefaultLimits encodes conventional résumé advice: the most recent/most
// relevant role earns the most space and older roles taper down, with a floor
// so no kept role is gutted to a single anemic line. Caps is the soft target;
// MaxCaps is the hard ceiling a role may expand to when it carries more genuinely
// strong, on-target bullets than its cap (see Select) — so a must-have isn't
// evicted just to hit the cap. The whole section still aims for ~20 bullets.
var DefaultLimits = Limits{
	Caps:    []int{6, 5, 4, 3},
	Floors:  []int{4, 3, 2, 2},
	MaxCaps: []int{8, 7, 6, 5},
	Budget:  20,
}

// CapFor returns the per-role cap for the role at position i (0 = most recent),
// holding the final cap for every role beyond the slice.
func (l Limits) CapFor(i int) int {
	caps := l.Caps
	if len(caps) == 0 {
		caps = DefaultLimits.Caps
	}
	if i < len(caps) {
		return caps[i]
	}
	return caps[len(caps)-1]
}

// FloorFor returns the per-role minimum-to-keep for the role at position i,
// holding the final floor for every role beyond the slice.
func (l Limits) FloorFor(i int) int {
	floors := l.Floors
	if len(floors) == 0 {
		floors = DefaultLimits.Floors
	}
	if i < len(floors) {
		return floors[i]
	}
	return floors[len(floors)-1]
}

// MaxCapFor returns the hard expansion ceiling for the role at position i,
// holding the final value past the slice. Falls back to the cap when MaxCaps is
// unset, so expansion is simply disabled rather than panicking.
func (l Limits) MaxCapFor(i int) int {
	maxes := l.MaxCaps
	if len(maxes) == 0 {
		return l.CapFor(i)
	}
	if i < len(maxes) {
		return maxes[i]
	}
	return maxes[len(maxes)-1]
}

func (l Limits) budget() int {
	if l.Budget <= 0 {
		return DefaultLimits.Budget
	}
	return l.Budget
}

// RoleFinding is the per-role density verdict.
type RoleFinding struct {
	RoleID  string
	Title   string
	Company string
	Dates   string
	Count   int
	Cap     int
	Floor   int
	// Over is Count-Cap when the role exceeds its cap, else 0 — the number of
	// bullets to trim from this role.
	Over int
	// Under is Floor-Count when the role falls below its floor, else 0 — the
	// number of bullets the role is short of a respectable minimum.
	Under int
	// Message is a one-line, human-readable suggestion. Empty when the role is
	// within [Floor, Cap].
	Message string
}

// OverCap reports whether the role carries more bullets than its cap.
func (f RoleFinding) OverCap() bool { return f.Over > 0 }

// UnderFloor reports whether the role carries fewer bullets than its floor.
func (f RoleFinding) UnderFloor() bool { return f.Under > 0 }

// Flagged reports whether the role is outside its [Floor, Cap] band.
func (f RoleFinding) Flagged() bool { return f.Over > 0 || f.Under > 0 }

// Report is the full density analysis.
type Report struct {
	Roles []RoleFinding
	// Total is the experience-bullet count across every role.
	Total int
	// Budget is the recommended ceiling on Total.
	Budget int
	// OverBudget is Total-Budget when the section runs long, else 0.
	OverBudget int
	// TrimTotal is the sum of every role's Over — the smallest number of cuts
	// that brings every role within its cap.
	TrimTotal int
	// PadTotal is the sum of every role's Under — bullets the thin roles are
	// short of their floor.
	PadTotal int
	// Verdict is a one-line overall summary, always set.
	Verdict string
}

// HasFindings reports whether anything is worth surfacing — a role over its
// cap, a role under its floor, or the section over budget.
func (r Report) HasFindings() bool { return r.TrimTotal > 0 || r.PadTotal > 0 || r.OverBudget > 0 }

// Analyze applies the density heuristic to roles (in résumé order, most recent
// first) under the given limits. Pass DefaultLimits for the conventional caps.
func Analyze(roles []RoleBullets, limits Limits) Report {
	rep := Report{Budget: limits.budget()}
	for i, role := range roles {
		cap := limits.CapFor(i)
		floor := limits.FloorFor(i)
		f := RoleFinding{
			RoleID:  role.RoleID,
			Title:   role.Title,
			Company: role.Company,
			Dates:   role.Dates,
			Count:   role.Count,
			Cap:     cap,
			Floor:   floor,
		}
		rep.Total += role.Count
		switch {
		case role.Count > cap:
			f.Over = role.Count - cap
			rep.TrimTotal += f.Over
			f.Message = fmt.Sprintf("%d bullets — recommend ≤%d for %s; trim ~%d.",
				role.Count, cap, positionPhrase(i, len(roles)), f.Over)
		case role.Count < floor:
			f.Under = floor - role.Count
			rep.PadTotal += f.Under
			f.Message = fmt.Sprintf("%d bullets — thin for %s; aim for ≥%d.",
				role.Count, positionPhrase(i, len(roles)), floor)
		}
		rep.Roles = append(rep.Roles, f)
	}
	if rep.Total > rep.Budget {
		rep.OverBudget = rep.Total - rep.Budget
	}
	rep.Verdict = verdict(rep)
	return rep
}

// positionPhrase describes a role's seniority slot for the suggestion text.
func positionPhrase(i, n int) string {
	switch {
	case i == 0:
		return "the most recent role"
	case i == n-1 && n > 2:
		return "the oldest role"
	default:
		return "an older role"
	}
}

// verdict summarises the report in one line.
func verdict(r Report) string {
	var overCount, underCount int
	for _, f := range r.Roles {
		if f.OverCap() {
			overCount++
		}
		if f.UnderFloor() {
			underCount++
		}
	}
	switch {
	case overCount > 0 && r.OverBudget > 0:
		return fmt.Sprintf("Too many bullets: %d role(s) over cap and %d over the %d-bullet page budget — trim ~%d.",
			overCount, r.OverBudget, r.Budget, max(r.TrimTotal, r.OverBudget))
	case overCount > 0:
		return fmt.Sprintf("Too many bullets in %d role(s) — trim ~%d to tighten.", overCount, r.TrimTotal)
	case r.OverBudget > 0:
		return fmt.Sprintf("Per-role counts are fine, but %d bullets total exceeds the %d-bullet budget — trim ~%d.",
			r.Total, r.Budget, r.OverBudget)
	case underCount > 0:
		return fmt.Sprintf("%d role(s) look thin — add ~%d bullet(s) or fold them in.", underCount, r.PadTotal)
	default:
		return "Bullet density looks good."
	}
}
