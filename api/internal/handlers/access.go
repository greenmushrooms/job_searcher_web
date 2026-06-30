package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
)

// ParseProfileAccess parses the PROFILE_ACCESS env value — a comma-separated
// list of remoteUser:profile pairs, e.g. "slava:Slava,kezia:Kezia" — into a
// lookup from the authenticated identity (lowercased) to the one profile that
// identity may see. Surrounding whitespace is ignored. An empty value yields a
// nil map, which RestrictProfile treats as "isolation disabled".
func ParseProfileAccess(s string) (map[string]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	m := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		user, profile, ok := strings.Cut(pair, ":")
		user = strings.ToLower(strings.TrimSpace(user))
		profile = strings.TrimSpace(profile)
		if !ok || user == "" || profile == "" {
			return nil, fmt.Errorf("PROFILE_ACCESS: invalid entry %q (want user:profile)", pair)
		}
		m[user] = profile
	}
	return m, nil
}

// RestrictProfile enforces per-user profile isolation from the trusted
// Remote-User header that the auth proxy (Authelia via Caddy) injects.
//
// Trust model: in production the app binds 127.0.0.1 and is reachable only
// through Caddy, which authenticates the user and sets Remote-User, so the
// header can't be spoofed by an external client. On the dev machine there is no
// proxy and no header, so requests stay unrestricted — preserving full
// multi-profile control locally.
//
//   - access map empty              → never restrict (PROFILE_ACCESS unset)
//   - Remote-User absent            → unrestricted (local dev / trusted operator)
//   - Remote-User mapped            → pin the request to that user's profile
//   - Remote-User present, unmapped → 403 (authenticated but not provisioned)
func RestrictProfile(access map[string]string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(access) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			user := strings.ToLower(strings.TrimSpace(r.Header.Get("Remote-User")))
			if user == "" {
				next.ServeHTTP(w, r)
				return
			}
			profile, ok := access[user]
			if !ok {
				writeErr(w, http.StatusForbidden, "no profile access provisioned for this account")
				return
			}
			next.ServeHTTP(w, r.WithContext(profiles.WithPinned(r.Context(), profile)))
		})
	}
}

// resolveWriteProfile picks the profile for a JSON write handler, honoring a
// per-user pin. On a pinned (isolated) request the pinned profile is forced and
// any requested value is ignored — so an isolated user can't write into another
// profile's namespace. Otherwise an empty request defaults to profiles.Default
// and a non-empty one must name a known profile, else a 400 is written and ok
// is false.
func resolveWriteProfile(w http.ResponseWriter, r *http.Request, requested string) (string, bool) {
	if pinned, ok := profiles.Pinned(r.Context()); ok {
		return pinned, true
	}
	if requested == "" {
		return profiles.Default, true
	}
	if !profiles.Valid(r.Context(), requested) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown profile", "valid": profiles.Known(r.Context())})
		return "", false
	}
	return requested, true
}
