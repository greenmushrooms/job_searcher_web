package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CoverLetterPromptVersion is logged into every cover_letter_drafted event,
// same retro contract as PromptVersion for resume drafts.
// cl-v1: first cut — grounded in résumé markdown, placeholders for personal
// content, 180–280 words, plain text out.
const CoverLetterPromptVersion = "cl-v1"

// coverLetterSystemPrompt frames the drafting task. Plain-text output (no
// JSON schema) — the whole reply is the letter.
const coverLetterSystemPrompt = `You write the first draft of a job-application cover letter.

Ground rules:
- Use ONLY facts present in the candidate's résumé. Never invent experience, employers, metrics, or technologies.
- Match the letter to the posting: pick the 2-3 strongest, most relevant achievements from the résumé and connect them to what the job asks for.
- Tone: professional, direct, confident. No clichés ("I am writing to express…", "fast-paced environment"), no flattery padding, no first-person gushing.
- Structure: greeting ("Dear Hiring Manager," unless the posting names someone); a 1-2 sentence opener naming the role and company; one or two short body paragraphs tying achievements to the role; a brief closing with a call to action and the candidate's name from the résumé. 180-280 words total.
- Where something personal is needed that the résumé cannot supply (why this company, relocation, notice period), leave a [bracketed placeholder] for the candidate to fill in.
- Output plain text only: the letter itself, no markdown, no subject line, no commentary before or after.`

type CoverLetterResult struct {
	Text          string `json:"text"`
	Usage         Usage  `json:"usage"`
	Model         string `json:"model"`
	PromptVersion string `json:"prompt_version"`
}

// CoverLetter sends the job posting plus the candidate's résumé markdown to
// the LLM and returns a first-draft cover letter as plain text.
func (c *Client) CoverLetter(ctx context.Context, title, company, jobDescription, resumeMarkdown string) (*CoverLetterResult, error) {
	prompt := buildCoverLetterPrompt(title, company, jobDescription, resumeMarkdown)

	reqBody := chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: coverLetterSystemPrompt},
			{Role: "user", Content: prompt},
		},
		// No ResponseFormat — the reply is the letter, not JSON.
		Temperature: 0.6, // prose task, want some fluency
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
	text := strings.TrimSpace(resp.Choices[0].Message.Content)
	if text == "" {
		return nil, fmt.Errorf("model returned an empty letter")
	}

	usage := Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
		CostUSD:          estimateCost(c.Model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens),
	}
	return &CoverLetterResult{
		Text:          text,
		Usage:         usage,
		Model:         c.Model,
		PromptVersion: CoverLetterPromptVersion,
	}, nil
}

// buildCoverLetterPrompt assembles the user message: the posting, then the
// résumé the letter must stay grounded in.
func buildCoverLetterPrompt(title, company, jobDescription, resumeMarkdown string) string {
	var b strings.Builder
	b.WriteString("=== JOB ===\n")
	b.WriteString("Title: ")
	b.WriteString(strings.TrimSpace(title))
	b.WriteString("\nCompany: ")
	b.WriteString(strings.TrimSpace(company))
	b.WriteString("\n\n=== JOB DESCRIPTION ===\n")
	b.WriteString(strings.TrimSpace(jobDescription))
	b.WriteString("\n\n=== CANDIDATE RÉSUMÉ (markdown) ===\n")
	b.WriteString(strings.TrimSpace(resumeMarkdown))
	b.WriteString("\n\nWrite the cover letter now.")
	return b.String()
}
