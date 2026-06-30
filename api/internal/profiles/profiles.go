// Package profiles validates the ?profile / form profile parameter against the
// set of profiles that actually exist in the pipeline (distinct sys_profile in
// public.evaluated_jobs). Without this, a typo'd profile silently reads and
// writes an empty namespace.
//
// The known set is cached in-process with a TTL so validation costs no DB
// round-trip per request, while still picking up new pipeline profiles without
// a restart. A process-global cache (initialised once from main) keeps handlers
// from each needing to carry a validator dependency.
package profiles

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Default is the fallback profile: used when none is supplied, and (in the UI)
// when an unknown one is. It is always treated as valid so the app works even
// before any jobs are evaluated.
const Default = "Slava"

// Lister is the subset of jobs.Repo this package needs.
type Lister interface {
	Profiles(ctx context.Context) ([]string, error)
}

var (
	mu     sync.RWMutex
	lister Lister
	ttl    = 5 * time.Minute
	cache  map[string]bool
	exp    time.Time
)

// Init wires the data source. Call once at startup before serving requests.
func Init(l Lister) {
	mu.Lock()
	lister = l
	mu.Unlock()
}

// known returns the cached profile set, refreshing it once past the TTL. On a
// DB error it returns the last good cache (or just the default) so a transient
// failure degrades to "default only" rather than rejecting everything.
func known(ctx context.Context) map[string]bool {
	mu.RLock()
	if cache != nil && time.Now().Before(exp) {
		c := cache
		mu.RUnlock()
		return c
	}
	mu.RUnlock()

	mu.Lock()
	defer mu.Unlock()
	if cache != nil && time.Now().Before(exp) { // re-check under write lock
		return cache
	}

	set := map[string]bool{Default: true}
	if lister != nil {
		if list, err := lister.Profiles(ctx); err == nil {
			for _, p := range list {
				set[p] = true
			}
		} else if cache != nil {
			return cache
		}
	}
	cache = set
	exp = time.Now().Add(ttl)
	return set
}

// Valid reports whether p is a known profile. Empty is not valid. On a pinned
// (isolated) request, only the pinned profile is valid.
func Valid(ctx context.Context, p string) bool {
	if pinned, ok := Pinned(ctx); ok {
		return p == pinned
	}
	return p != "" && known(ctx)[p]
}

// Known returns the sorted list of known profiles, for error messages and the
// UI profile switcher. On a pinned request it lists only the pinned profile, so
// the switcher collapses to a single option and other profiles aren't leaked.
func Known(ctx context.Context) []string {
	if pinned, ok := Pinned(ctx); ok {
		return []string{pinned}
	}
	set := known(ctx)
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Resolve returns p if it is a known profile, otherwise the default. Used by
// the htmx UI, where silently falling back is friendlier than erroring in the
// middle of a fragment swap. On a pinned (isolated) request it always returns
// the pinned profile, ignoring p — this is how an out-of-scope ?profile= is
// forced back to the user's own profile. The pinned profile is returned as-is
// (never falls back to Default) so an as-yet-unevaluated profile shows its own
// empty namespace rather than leaking the default profile's data.
func Resolve(ctx context.Context, p string) string {
	if pinned, ok := Pinned(ctx); ok {
		return pinned
	}
	if p == "" {
		return Default
	}
	if known(ctx)[p] {
		return p
	}
	return Default
}
