package profiles

import (
	"context"
	"testing"
)

func TestPinnedRoundTrip(t *testing.T) {
	if p, ok := Pinned(context.Background()); ok || p != "" {
		t.Fatalf("unpinned context: got (%q,%v), want (\"\",false)", p, ok)
	}
	ctx := WithPinned(context.Background(), "Kezia")
	if p, ok := Pinned(ctx); !ok || p != "Kezia" {
		t.Fatalf("pinned context: got (%q,%v), want (Kezia,true)", p, ok)
	}
	// An empty pin is treated as no pin, so it can't accidentally lock a user
	// out of every profile.
	if p, ok := Pinned(WithPinned(context.Background(), "")); ok || p != "" {
		t.Fatalf("empty pin: got (%q,%v), want (\"\",false)", p, ok)
	}
}

func TestResolveHonorsPin(t *testing.T) {
	pinned := WithPinned(context.Background(), "Kezia")
	// Any requested profile — including the default and an out-of-scope one — is
	// forced back to the pinned profile.
	for _, req := range []string{"", "Slava", "Kezia", "Nope"} {
		if got := Resolve(pinned, req); got != "Kezia" {
			t.Errorf("Resolve(pinned, %q) = %q, want Kezia", req, got)
		}
	}
	// Unpinned keeps the old behavior: empty -> default, known -> itself.
	bg := context.Background()
	if got := Resolve(bg, ""); got != Default {
		t.Errorf("Resolve(unpinned, \"\") = %q, want %q", got, Default)
	}
	if got := Resolve(bg, Default); got != Default {
		t.Errorf("Resolve(unpinned, %q) = %q, want itself", Default, got)
	}
}

func TestValidHonorsPin(t *testing.T) {
	pinned := WithPinned(context.Background(), "Kezia")
	if !Valid(pinned, "Kezia") {
		t.Error("Valid(pinned, Kezia) = false, want true")
	}
	for _, req := range []string{"", "Slava", "Nope"} {
		if Valid(pinned, req) {
			t.Errorf("Valid(pinned, %q) = true, want false", req)
		}
	}
}

func TestKnownHonorsPin(t *testing.T) {
	got := Known(WithPinned(context.Background(), "Kezia"))
	if len(got) != 1 || got[0] != "Kezia" {
		t.Errorf("Known(pinned) = %v, want [Kezia]", got)
	}
}
