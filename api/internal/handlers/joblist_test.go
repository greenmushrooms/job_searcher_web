package handlers

import "testing"

func ptr[T any](v T) *T { return &v }

func TestDateOnly(t *testing.T) {
	tests := []struct {
		in   *string
		want string
	}{
		{nil, "?"},
		{ptr(""), ""},
		{ptr("2026-05-29"), "2026-05-29"},
		{ptr("2026-05-29T12:34:56Z"), "2026-05-29"},
		{ptr("short"), "short"}, // < 10 chars: returned as-is
	}
	for _, tc := range tests {
		if got := dateOnly(tc.in); got != tc.want {
			t.Errorf("dateOnly(%v): got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFmtScore(t *testing.T) {
	if got := fmtScore(nil); got != "—" {
		t.Errorf("nil score: got %q, want —", got)
	}
	if got := fmtScore(ptr(7.25)); got != "7.2" && got != "7.3" {
		t.Errorf("7.25: got %q, want one-decimal rounding", got)
	}
	if got := fmtScore(ptr(8.0)); got != "8.0" {
		t.Errorf("8.0: got %q, want 8.0", got)
	}
}

func TestDerefOr(t *testing.T) {
	if got := derefOr(nil, "fb"); got != "fb" {
		t.Errorf("nil: got %q, want fb", got)
	}
	if got := derefOr(ptr(""), "fb"); got != "fb" {
		t.Errorf("empty: got %q, want fb", got)
	}
	if got := derefOr(ptr("x"), "fb"); got != "x" {
		t.Errorf("value: got %q, want x", got)
	}
}

func TestClampInt(t *testing.T) {
	tests := []struct{ v, lo, hi, want int }{
		{5, 1, 10, 5},
		{0, 1, 10, 1},
		{99, 1, 10, 10},
		{1, 1, 10, 1},
		{10, 1, 10, 10},
	}
	for _, tc := range tests {
		if got := clampInt(tc.v, tc.lo, tc.hi); got != tc.want {
			t.Errorf("clampInt(%d,%d,%d): got %d, want %d", tc.v, tc.lo, tc.hi, got, tc.want)
		}
	}
}

func TestReasoningStr(t *testing.T) {
	if got := reasoningStr(nil, "k"); got != "" {
		t.Errorf("nil map: got %q, want empty", got)
	}
	m := map[string]any{"verdict": "strong", "score": 7.0}
	if got := reasoningStr(m, "verdict"); got != "strong" {
		t.Errorf("verdict: got %q, want strong", got)
	}
	if got := reasoningStr(m, "missing"); got != "" {
		t.Errorf("missing key: got %q, want empty", got)
	}
	if got := reasoningStr(m, "score"); got != "" {
		t.Errorf("non-string value: got %q, want empty", got)
	}
}
