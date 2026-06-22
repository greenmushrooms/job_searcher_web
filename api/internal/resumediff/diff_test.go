package resumediff

import (
	"strings"
	"testing"
)

// kinds re-runs the line alignment and returns the per-row kind sequence, so
// tests can assert the structure of a diff without caring about rendered HTML.
func kinds(a, b string) []string {
	rows := computeRows(splitLines(a), splitLines(b))
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.kind
	}
	return out
}

func eqStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestIdenticalIsAllSame(t *testing.T) {
	doc := "- one\n- two\n- three"
	got := kinds(doc, doc)
	want := []string{"same", "same", "same"}
	if !eqStr(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for _, r := range Render(doc, doc) {
		if r.Left.Class != "" || r.Right.Class != "" {
			t.Fatalf("identical docs produced a highlighted cell: %+v", r)
		}
	}
}

func TestPureDeletionDoesNotSmear(t *testing.T) {
	// Removing the middle bullet must mark ONLY that line as del — the lines
	// after it stay "same" (the bug that motivated the similarity alignment).
	a := "- alpha\n- bravo\n- charlie"
	b := "- alpha\n- charlie"
	got := kinds(a, b)
	want := []string{"same", "del", "same"}
	if !eqStr(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
}

func TestPureInsertion(t *testing.T) {
	a := "- alpha\n- charlie"
	b := "- alpha\n- bravo\n- charlie"
	got := kinds(a, b)
	want := []string{"same", "add", "same"}
	if !eqStr(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
}

func TestEditedLineIsSubWithWordMarks(t *testing.T) {
	a := "Reduced monthly AWS costs by 40% through architecture."
	b := "Reduced monthly AWS costs through architecture."
	got := kinds(a, b)
	if !eqStr(got, []string{"sub"}) {
		t.Fatalf("kinds = %v, want [sub]", got)
	}
	rows := Render(a, b)
	if len(rows) != 1 || rows[0].Left.Class != "chg" || rows[0].Right.Class != "chg" {
		t.Fatalf("expected one chg/chg row, got %+v", rows)
	}
	// The deleted words ("by 40% ") must be inside a highlight span on the left,
	// and the unchanged tail must NOT be highlighted.
	left := string(rows[0].Left.HTML)
	if !strings.Contains(left, `<span class="d">`) {
		t.Fatalf("left cell has no word highlight: %q", left)
	}
	if strings.Contains(left, `<span class="d">through`) {
		t.Fatalf("unchanged words were highlighted: %q", left)
	}
}

func TestDissimilarReplaceNotWordDiffed(t *testing.T) {
	// Two unrelated lines in the same changed gap should become a whole-line
	// replace (del+add), never a word-diffed substitution.
	a := "- Built Kafka streaming pipelines for live events."
	b := "- Designed Snowflake dimensional models for finance."
	got := kinds(a, b)
	if !eqStr(got, []string{"replace"}) {
		t.Fatalf("kinds = %v, want [replace]", got)
	}
	rows := Render(a, b)
	if rows[0].Left.Class != "del" || rows[0].Right.Class != "add" {
		t.Fatalf("expected del/add replace row, got %+v", rows)
	}
}

func TestDeleteStackedOnEditNotMerged(t *testing.T) {
	// A removed line sitting above a wholesale-rewritten line must NOT be paired
	// into a single "replace" row with the rewrite's new text — a delete and an
	// edit are two independent changes and must render as separate rows. (The
	// old index-pairing merged the first delete with the first add.)
	a := "- keep this line\n- alpha bravo charlie delta\n- echo foxtrot golf hotel"
	b := "- keep this line\n- zulu yankee xray whiskey"
	got := kinds(a, b)
	want := []string{"same", "del", "del", "add"}
	if !eqStr(got, want) {
		t.Fatalf("kinds = %v, want %v (delete + edit must not merge into one replace)", got, want)
	}
	for _, r := range Render(a, b) {
		if r.Left.Class == "del" && r.Right.Class == "add" {
			t.Fatalf("unrelated delete and add merged into one replace row: %+v", r)
		}
	}
}

func TestMultibyteWordOffsets(t *testing.T) {
	// Em dashes (—) are multibyte; rune-based offsets must keep the highlight
	// aligned to the changed word and produce valid, escaped HTML.
	a := "Senior Engineer — owns billing pipelines end to end."
	b := "Senior Engineer — owns usage pipelines end to end."
	rows := Render(a, b)
	if len(rows) != 1 || rows[0].Left.Class != "chg" {
		t.Fatalf("expected single chg row, got %+v", rows)
	}
	left := string(rows[0].Left.HTML)
	if !strings.Contains(left, `<span class="d">billing</span>`) {
		t.Fatalf("expected 'billing' highlighted on left, got %q", left)
	}
	if !strings.Contains(left, "—") {
		t.Fatalf("em dash mangled: %q", left)
	}
}

func TestHTMLIsEscaped(t *testing.T) {
	a := "value < 40 & rising"
	b := "value < 50 & rising"
	rows := Render(a, b)
	out := string(rows[0].Left.HTML)
	if strings.Contains(out, "<40") || !strings.Contains(out, "&lt;") || !strings.Contains(out, "&amp;") {
		t.Fatalf("HTML not escaped: %q", out)
	}
}

func TestCRLFNormalisedNoPhantomDiff(t *testing.T) {
	// One side stored with CRLF (DB), the other typed/normalised to LF must not
	// report every line as changed.
	a := "- one\r\n- two\r\n- three"
	b := "- one\n- two\n- three"
	got := kinds(a, b)
	want := []string{"same", "same", "same"}
	if !eqStr(got, want) {
		t.Fatalf("CRLF vs LF kinds = %v, want %v", got, want)
	}
}
