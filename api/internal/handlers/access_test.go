package handlers

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
)

func TestParseProfileAccess(t *testing.T) {
	got, err := ParseProfileAccess(" slava:Slava , Kezia:Kezia ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"slava": "Slava", "kezia": "Kezia"} // user lowercased, profile kept as-is
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	if m, err := ParseProfileAccess("   "); err != nil || m != nil {
		t.Errorf("empty value: got (%v,%v), want (nil,nil)", m, err)
	}

	for _, bad := range []string{"slava", "slava:", ":Slava", "a:b,c"} {
		if _, err := ParseProfileAccess(bad); err == nil {
			t.Errorf("ParseProfileAccess(%q): expected error", bad)
		}
	}
}

// pinProbe records the profile pinned on the request it receives.
func pinProbe(seen *string, pinned *bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*seen, *pinned = profiles.Pinned(r.Context())
		w.WriteHeader(http.StatusOK)
	}
}

func TestRestrictProfile(t *testing.T) {
	access := map[string]string{"slava": "Slava", "kezia": "Kezia"}

	tests := []struct {
		name       string
		access     map[string]string
		remoteUser string
		wantStatus int
		wantPinned bool
		wantSeen   string
	}{
		{"disabled lets header through unpinned", nil, "kezia", http.StatusOK, false, ""},
		{"no header is unrestricted", access, "", http.StatusOK, false, ""},
		{"mapped user is pinned", access, "kezia", http.StatusOK, true, "Kezia"},
		{"mapped user case-insensitive", access, "SLAVA", http.StatusOK, true, "Slava"},
		{"unmapped user forbidden", access, "intruder", http.StatusForbidden, false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var seen string
			var pinned bool
			h := RestrictProfile(tc.access)(pinProbe(&seen, &pinned))

			req := httptest.NewRequest(http.MethodGet, "/ui/jobs?profile=Slava", nil)
			if tc.remoteUser != "" {
				req.Header.Set("Remote-User", tc.remoteUser)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantStatus != http.StatusOK {
				return // downstream handler never ran
			}
			if pinned != tc.wantPinned || seen != tc.wantSeen {
				t.Errorf("pinned=(%q,%v), want (%q,%v)", seen, pinned, tc.wantSeen, tc.wantPinned)
			}
		})
	}
}
