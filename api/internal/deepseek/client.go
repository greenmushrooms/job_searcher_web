// Package deepseek wraps the OpenAI-compatible /chat/completions endpoint
// at https://api.deepseek.com. Used by the draft-resume handler to get
// per-bullet keep/drop decisions from the LLM.
//
// We hit the HTTP API directly rather than pull in an SDK because the
// contract is tiny — one POST, one structured JSON reply.
package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

// PromptVersion is logged into every resume_drafted event so we can later
// retro the LLM's behaviour against the exact prompt that produced it.
// Bump when the prompt template, system instruction, or output schema changes.
// v2: removals-only schema (was per-bullet keep/drop decisions).
// v3: adds per-bullet rewrites alongside removals.
// v4: rewrites must be concise resume-style fragments, not prose sentences.
// v5: relevance guidance from the 2026-06-12 eval — judge by transferable
//     competency not surface domain, awards keep-by-default, prune oldest
//     roles hardest/consistently, reword toward the posting's stack without
//     inventing tools.
// v6: floor on rewrites — surface at least two or three of the strongest
//     rewrite opportunities so a job far from the candidate's domain never
//     comes back with a single suggestion. Removals stay conservative.
// v7: relevance-first rewrites — replaces v6's count floor. Each rewrite's
//     reason must name the specific posting requirement it serves (grammar/
//     tense-only reasons are rejected), must foreground must-have tools the
//     candidate genuinely has, and may use ONLY tools already present in the
//     bullet text (no introducing/swapping tools). Quality over quantity.
// v8: education entries can now be pruned. The model sees the education list
//     and may drop SUPPLEMENTARY entries (professional exams, certifications,
//     standalone courses) when clearly irrelevant to the posting. Degrees and
//     diplomas are keep-by-default and never listed.
const PromptVersion = "v8"

// Pricing per 1M tokens, USD. Cache-miss prices (worst case). Updated 2026-05-25
// from the DeepSeek pricing docs. Pricing is in flux (V4 launch had a 75%
// discount active); these numbers are the "rack rate" so cost_usd in events
// is a conservative upper bound.
type pricing struct{ inputPer1M, outputPer1M float64 }

var modelPricing = map[string]pricing{
	"deepseek-v4-pro":   {inputPer1M: 0.435, outputPer1M: 0.87},
	"deepseek-v4-flash": {inputPer1M: 0.14, outputPer1M: 0.28},
	// Legacy aliases — same backend as v4-flash per DeepSeek docs.
	"deepseek-chat":     {inputPer1M: 0.14, outputPer1M: 0.28},
	"deepseek-reasoner": {inputPer1M: 0.14, outputPer1M: 0.28},
}

// Removal is one bullet the LLM recommends dropping for this job. Bullets not
// listed in the draft's removals are kept by default — the render starts from
// the full resume and treats removals as a diff over it.
type Removal struct {
	RoleID   string `json:"role_id"`
	BulletID string `json:"bullet_id"`
	Reason   string `json:"reason"`
}

// Rewrite is one bullet the LLM suggests rewording for this job. NewText is the
// improved bullet; the original stays the source of truth until the user
// accepts it. Bullets not listed keep their canonical text.
type Rewrite struct {
	RoleID   string `json:"role_id"`
	BulletID string `json:"bullet_id"`
	NewText  string `json:"new_text"`
	Reason   string `json:"reason"`
}

// EducationRemoval is one SUPPLEMENTARY education entry the LLM recommends
// dropping for this job (e.g. a professional exam or certification that's
// irrelevant here). Degrees are never listed. Entries not listed are kept.
type EducationRemoval struct {
	EducationID string `json:"education_id"`
	Reason      string `json:"reason"`
}

// Usage mirrors the chat-completions response.usage object.
type Usage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

type DraftResult struct {
	Removals          []Removal          `json:"removals"`
	Rewrites          []Rewrite          `json:"rewrites"`
	EducationRemovals []EducationRemoval `json:"education_removals"`
	Usage             Usage              `json:"usage"`
	Model             string             `json:"model"`
	PromptVersion     string             `json:"prompt_version"`
}

type Client struct {
	HTTPClient *http.Client
	APIKey     string
	BaseURL    string // e.g. https://api.deepseek.com
	Model      string // e.g. deepseek-v4-pro
}

// NewFromEnv reads DEEPSEEK_API_KEY, DEEPSEEK_BASE_URL, DEEPSEEK_MODEL.
// Returns ErrNotConfigured if API key is missing — handlers should 503 in
// that case rather than crash the whole server at boot.
func NewFromEnv() (*Client, error) {
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		return nil, ErrNotConfigured
	}
	base := os.Getenv("DEEPSEEK_BASE_URL")
	if base == "" {
		base = "https://api.deepseek.com"
	}
	model := os.Getenv("DEEPSEEK_MODEL")
	if model == "" {
		model = "deepseek-v4-pro"
	}
	return &Client{
		// DeepSeek v4-pro on a ~40-bullet pool ranges 60–110s+; keep this just
		// under the 180s route timeout so the route never wins the race.
		HTTPClient: &http.Client{Timeout: 170 * time.Second},
		APIKey:     key,
		BaseURL:    strings.TrimRight(base, "/"),
		Model:      model,
	}, nil
}

var ErrNotConfigured = errors.New("DEEPSEEK_API_KEY not set")

// Draft sends the job description + active bullet pool (and supplementary
// education entries) to the LLM and returns what it recommends removing or
// rewriting for this job.
func (c *Client) Draft(ctx context.Context, jobDescription string, bullets []resume.Bullet, education []resume.DocEducation) (*DraftResult, error) {
	prompt := buildPrompt(jobDescription, bullets, education)

	reqBody := chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
		ResponseFormat: &responseFormat{Type: "json_object"},
		Temperature:    0.2, // structured task, want stability
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
		Removals          []Removal          `json:"removals"`
		Rewrites          []Rewrite          `json:"rewrites"`
		EducationRemovals []EducationRemoval `json:"education_removals"`
	}
	content := resp.Choices[0].Message.Content
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("decode draft JSON: %w (content=%s)", err, truncate(content, 500))
	}

	usage := Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
		CostUSD:          estimateCost(c.Model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens),
	}
	return &DraftResult{
		Removals:          parsed.Removals,
		Rewrites:          parsed.Rewrites,
		EducationRemovals: parsed.EducationRemovals,
		Usage:             usage,
		Model:             c.Model,
		PromptVersion:     PromptVersion,
	}, nil
}

func (c *Client) post(ctx context.Context, path string, body any) ([]byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deepseek http: %w", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("deepseek status %d: %s", resp.StatusCode, truncate(string(out), 500))
	}
	return out, nil
}

func estimateCost(model string, inTok, outTok int) float64 {
	p, ok := modelPricing[model]
	if !ok {
		// Unknown model — fall back to v4-pro pricing so cost isn't silently zero.
		p = modelPricing["deepseek-v4-pro"]
	}
	return (float64(inTok)*p.inputPer1M + float64(outTok)*p.outputPer1M) / 1_000_000
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ── Request/response shapes for the OpenAI-compatible API ───────────────────

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Temperature    float64         `json:"temperature,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}
