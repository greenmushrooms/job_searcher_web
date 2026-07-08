package handlers

import (
	"net/http"

	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
)

// DemoReadOnly is the whole security model of the public sample instance
// (sample.jobs.*): every request is pinned to one synthetic profile and
// anything that isn't a read is rejected before reaching a handler. The demo
// runs as a separate container with DEMO_PROFILE set and no auth proxy in
// front — it never sees a Remote-User header and never accepts a write, so
// real profiles are unreachable from it by construction.
func DemoReadOnly(profile string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				writeErr(w, http.StatusForbidden, "this is a read-only demo — changes are disabled")
				return
			}
			next.ServeHTTP(w, r.WithContext(profiles.WithPinned(r.Context(), profile)))
		})
	}
}
