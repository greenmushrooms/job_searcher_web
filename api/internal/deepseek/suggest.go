package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SuggestVersion identifies the search-suggestion prompt, so behaviour changes
// are traceable the same way PromptVersion/ScorerVersion are.
// v1: 4–8 realistic job-board queries from the master résumé; locations from
//
//	the résumé, falling back to the profile's existing searches.
const SuggestVersion = "v1"

// suggestModel is flash by default: like scoring, this is a small atomic task
// (one short completion over one document) where flash is plenty, independent
// of whichever model DEEPSEEK_MODEL selects for tailoring.
func suggestModel() string {
	if m := os.Getenv("DEEPSEEK_SUGGEST_MODEL"); m != "" {
		return m
	}
	return "deepseek-v4-flash"
}

// SearchSuggestion is one proposed scrape query for the settings page.
type SearchSuggestion struct {
	Title    string `json:"title"`
	Location string `json:"location"`
	Reason   string `json:"reason"`
}

const suggestSystemPrompt = `You suggest job-board search queries for a candidate based on their résumé. Return ONLY JSON: {"suggestions":[{"title":"...","location":"...","reason":"..."}]}.

Rules:
- 4 to 8 suggestions.
- "title" is a short, realistic query someone would type into a job board's title box (e.g. "data engineer", "senior backend developer") — 2 to 5 words. Ground every title in roles or skills the résumé actually demonstrates; do not inflate seniority beyond its evidence.
- Cover a spread: the candidate's core title(s) first, then close adjacent titles they plausibly qualify for.
- "location" is the candidate's city/region as the résumé states it (e.g. "Toronto, ON"). If the résumé gives no location, reuse the most common location from CURRENT SEARCHES; if there are none, use "Remote".
- Never repeat a (title, location) pair already in CURRENT SEARCHES.
- "reason" is one short clause naming the résumé evidence for the title.`

// SuggestSearches proposes job-board queries from the master résumé. existing
// is the profile's current searches as preformatted "title — location" lines,
// so the model can avoid duplicates and borrow a location when the résumé
// doesn't state one.
func (c *Client) SuggestSearches(ctx context.Context, resumeMarkdown string, existing []string) ([]SearchSuggestion, error) {
	current := "none yet"
	if len(existing) > 0 {
		current = strings.Join(existing, "\n")
	}
	prompt := "=== RÉSUMÉ (markdown) ===\n" + strings.TrimSpace(resumeMarkdown) +
		"\n\n=== CURRENT SEARCHES ===\n" + current +
		"\n\nSuggest job-board search queries for this candidate."

	reqBody := chatRequest{
		Model: suggestModel(),
		Messages: []chatMessage{
			{Role: "system", Content: suggestSystemPrompt},
			{Role: "user", Content: prompt},
		},
		ResponseFormat: &responseFormat{Type: "json_object"},
		Temperature:    0.3, // mostly deterministic, small variety in adjacent titles
	}
	raw, err := c.post(ctx, "/chat/completions", reqBody)
	if err != nil {
		return nil, err
	}

	var resp chatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode chat response: %w (body=%s)", err, truncate(string(raw), 500))
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("empty choices in chat response: %s", truncate(string(raw), 500))
	}

	var parsed struct {
		Suggestions []SearchSuggestion `json:"suggestions"`
	}
	content := resp.Choices[0].Message.Content
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("decode suggestions JSON: %w (content=%s)", err, truncate(content, 500))
	}
	return parsed.Suggestions, nil
}
