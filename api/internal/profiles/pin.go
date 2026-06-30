package profiles

import "context"

// pinKeyType is an unexported context-key type so the pin can't collide with
// keys set by other packages.
type pinKeyType struct{}

var pinKey pinKeyType

// WithPinned returns a child context restricted to a single profile. A request
// carrying a pin is isolated: Resolve always returns the pinned profile
// (ignoring any ?profile=), Valid accepts only it, and Known lists only it.
// The Remote-User access middleware sets the pin to enforce per-user isolation;
// requests without a pin (local dev, no auth proxy) keep full multi-profile
// access.
func WithPinned(ctx context.Context, profile string) context.Context {
	return context.WithValue(ctx, pinKey, profile)
}

// Pinned returns the profile this request is restricted to, if any. ok is false
// for unrestricted requests, so handlers that bypass Resolve (raw ?profile=
// reads, JSON write defaults) can force the pinned profile explicitly.
func Pinned(ctx context.Context) (string, bool) {
	p, ok := ctx.Value(pinKey).(string)
	return p, ok && p != ""
}
