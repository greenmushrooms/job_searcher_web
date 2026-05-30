package handlers

import (
	"net/http"
	"net/url"
	"testing"
)

func TestBuildTemplateBullets_LinkedVsOverride(t *testing.T) {
	canon := map[string]string{
		"r1.b1": "canonical one",
		"r1.b2": "canonical two",
		"r2.b9": "canonical nine",
	}
	form := url.Values{
		"text_r1.b1": {"canonical one"}, // unchanged -> linked (nil override)
		"text_r1.b2": {"reworded two"},  // edited -> override
		"text_r2.b9": {"   "},           // blank -> canonical fallback -> linked
	}
	r := &http.Request{Form: form}
	kept := []string{"r1.b1", "r1.b2", "r2.b9", "bad"}

	got := buildTemplateBullets(kept, r, canon)
	if len(got) != 3 {
		t.Fatalf("got %d, want 3 (bad id skipped): %+v", len(got), got)
	}

	byCID := map[string]int{}
	for i, b := range got {
		byCID[b.RoleID+"."+b.BulletID] = i
	}

	// b1: linked
	if b := got[byCID["r1.b1"]]; b.OverrideText != nil {
		t.Errorf("r1.b1 should be linked (nil override), got %q", *b.OverrideText)
	}
	// b2: override
	if b := got[byCID["r1.b2"]]; b.OverrideText == nil || *b.OverrideText != "reworded two" {
		t.Errorf("r1.b2 override: got %v, want \"reworded two\"", b.OverrideText)
	}
	// b9: blank -> canonical -> linked
	if b := got[byCID["r2.b9"]]; b.OverrideText != nil {
		t.Errorf("r2.b9 blank should fall back to canonical (linked), got %q", *b.OverrideText)
	}
	// sort order preserved (b1=0, b2=1, b9=2)
	if got[0].BulletID != "b1" || got[1].BulletID != "b2" || got[2].BulletID != "b9" {
		t.Errorf("sort order not preserved: %+v", got)
	}
}
