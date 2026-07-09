package handlers

import (
	"strings"
	"testing"

	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
	"github.com/greenmushrooms/job_searcher_web/api/internal/searchconfig"
)

func TestFilterSuggestions(t *testing.T) {
	existing := []searchconfig.Entry{
		{Title: "Data Engineer", Location: "Toronto, ON", Searches: 20},
	}
	sugs := []deepseek.SearchSuggestion{
		{Title: "data engineer", Location: "toronto, on"},         // dup of saved (case-insensitive)
		{Title: "  analytics engineer ", Location: " Toronto, ON"}, // kept, trimmed
		{Title: "analytics engineer", Location: "Toronto, ON"},    // dup within batch
		{Title: "", Location: "Toronto, ON"},                      // blank title
		{Title: "backend developer", Location: ""},                // blank location
		{Title: strings.Repeat("x", 81), Location: "Toronto, ON"}, // over form's 80-char limit
		{Title: "bi developer", Location: "Remote"},               // kept
	}
	got := filterSuggestions(sugs, existing)
	if len(got) != 2 {
		t.Fatalf("got %d suggestions, want 2: %+v", len(got), got)
	}
	if got[0].Title != "analytics engineer" || got[0].Location != "Toronto, ON" {
		t.Errorf("got[0] = %+v, want trimmed analytics engineer / Toronto, ON", got[0])
	}
	if got[1].Title != "bi developer" {
		t.Errorf("got[1] = %+v, want bi developer", got[1])
	}
}

func TestFilterSuggestionsCap(t *testing.T) {
	var sugs []deepseek.SearchSuggestion
	for i := 0; i < maxSuggestions+5; i++ {
		sugs = append(sugs, deepseek.SearchSuggestion{
			Title:    "title " + strings.Repeat("a", i+1),
			Location: "Remote",
		})
	}
	if got := filterSuggestions(sugs, nil); len(got) != maxSuggestions {
		t.Errorf("got %d suggestions, want cap %d", len(got), maxSuggestions)
	}
}
