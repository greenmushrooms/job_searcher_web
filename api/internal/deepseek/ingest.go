package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
)

// IngestPromptVersion tracks the résumé-structuring prompt separately from the
// tailoring prompt.
const IngestPromptVersion = "ingest-v1"

// ingestSystemPrompt turns a pasted résumé (any format — Word text, PDF text,
// markdown) into the canonical structured shape. Extraction ONLY: the model
// must never invent, merge, or embellish content — every claim in the output
// must appear in the input (résumé truthfulness is a hard requirement here).
const ingestSystemPrompt = `You are a résumé parser. You receive the raw text of one person's résumé and output ONLY a JSON object — no prose, no code fences — with this exact shape:

{
  "contact": {"name": "", "email": "", "phone": "", "github": "", "location": ""},
  "summary": "",
  "skills": [{"text": "", "category": ""}],
  "experience": [
    {"title": "", "company": "", "location": "", "dates": "",
     "bullets": [{"text": ""}]}
  ],
  "education": [{"degree": "", "institution": "", "location": ""}],
  "markdown": ""
}

Rules:
- EXTRACTION ONLY. Copy the résumé's own wording. Never invent, embellish,
  quantify, or merge claims. If a field is absent, use "" or [].
- Keep the résumé's own ordering (experience newest-first as written).
- bullets: one entry per achievement/duty line, text verbatim (normalize
  whitespace and strip the leading bullet glyph only).
- skills: one entry per skill or skill-group line; category is the résumé's
  own grouping heading if it has one, else "".
- summary: the résumé's own profile/objective paragraph, verbatim; "" if none.
- markdown: the same résumé rendered as clean GitHub-flavored markdown
  (# name, ## section headings, ### role — company lines, - bullets). Same
  content as the structured fields — a formatting pass, not a rewrite.`

// StructureResume extracts a pasted résumé into the canonical JSON shape and
// returns the raw JSON for the caller to decode (the struct lives in
// resumeingest; returning bytes avoids an import cycle).
func (c *Client) StructureResume(ctx context.Context, resumeText string) (json.RawMessage, *Usage, error) {
	reqBody := chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: ingestSystemPrompt},
			{Role: "user", Content: resumeText},
		},
		ResponseFormat: &responseFormat{Type: "json_object"},
		Temperature:    0.1, // pure extraction, want maximal fidelity
	}
	raw, err := c.post(ctx, "/chat/completions", reqBody)
	if err != nil {
		return nil, nil, err
	}

	var resp chatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, fmt.Errorf("decode chat response: %w (body=%s)", err, truncate(string(raw), 500))
	}
	if len(resp.Choices) == 0 {
		return nil, nil, fmt.Errorf("empty choices in chat response: %s", truncate(string(raw), 500))
	}

	usage := &Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
		CostUSD:          estimateCost(c.Model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens),
	}
	return json.RawMessage(resp.Choices[0].Message.Content), usage, nil
}
