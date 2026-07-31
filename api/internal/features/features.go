// Package features is the per-profile feature-flag lookup behind web.profile_features.
//
// Opt-in by construction: an absent row means OFF. That way a flag added to the
// shared box can never surface for a profile that didn't ask for it, and a
// lookup failure degrades to hiding the feature rather than exposing it.
package features

import (
	"context"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

// Practice gates the job workspace's practice-exercise section.
const Practice = "practice"

// Enabled reports whether the profile has the feature turned on. Any error —
// missing table on an unmigrated box, dead pool, cancelled context — reports
// false: for a gate, failing closed is the only safe direction.
func Enabled(ctx context.Context, q db.Querier, sysProfile, feature string) bool {
	if sysProfile == "" || feature == "" {
		return false
	}
	var on bool
	err := q.QueryRow(ctx, `
        SELECT enabled FROM web.profile_features
        WHERE sys_profile = $1 AND feature = $2
    `, sysProfile, feature).Scan(&on)
	return err == nil && on
}
