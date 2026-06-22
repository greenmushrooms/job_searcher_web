package resumesuggest

import "github.com/greenmushrooms/job_searcher_web/api/internal/resume"

// FromBullets folds a flat (role, bullet) pool — already in résumé order, most
// recent role first — into per-role counts for the density check and the
// tailoring prompt's per-role budget. The first time a role_id is seen fixes its
// position, so order matches the canonical résumé.
//
// This is the one adapter that couples resumesuggest to the resume package; the
// core analysis in density.go stays dependency-free and unit-testable on its own.
func FromBullets(bullets []resume.Bullet) []RoleBullets {
	var roles []RoleBullets
	idx := map[string]int{}
	for _, b := range bullets {
		i, ok := idx[b.RoleID]
		if !ok {
			i = len(roles)
			idx[b.RoleID] = i
			roles = append(roles, RoleBullets{
				RoleID:  b.RoleID,
				Title:   b.RoleTitle,
				Company: b.RoleCompany,
				Dates:   b.RoleDates,
			})
		}
		roles[i].Count++
	}
	return roles
}
