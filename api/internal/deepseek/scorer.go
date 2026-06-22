package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

// ScorerVersion identifies the relevance-scoring mechanism, logged into the
// resume_drafted event so a draft self-describes how its removals were chosen.
//
// v10: per-bullet relevance scoring. Each bullet is scored 0–100 against the
//
//	posting in isolation by deepseek-v4-flash under the "A_clean" prompt —
//	the winner of the 2026-06 prompt tournament (keep-overlap 0.80 / Spearman
//	0.79 vs hand-ranking; beat the heavier rubric, bare-minimal, pro model,
//	and a leak-free rewrite of the anchored prompt). Selection
//	(package resumesuggest) keeps the top score per role within [floor, cap].
const ScorerVersion = "v10"

// scorerModel is always flash: the calibration found flash matches/beats
// deepseek-v4-pro on this atomic single-bullet task at a fraction of the cost,
// independent of whichever model DEEPSEEK_MODEL selects for tailoring.
func scorerModel() string {
	if m := os.Getenv("DEEPSEEK_SCORER_MODEL"); m != "" {
		return m
	}
	return "deepseek-v4-flash"
}

// scorerSystemPrompt is the "A_clean" tournament winner verbatim: a generic
// 0–100 rubric with anchors and no bullet- or job-specific hints (the one
// example we tried leaked an answer and hurt rank fidelity, so it's gone).
const scorerSystemPrompt = `You score how relevant ONE résumé bullet is to a specific job posting, on a 0–100 scale. Judge by the underlying competency the bullet demonstrates and whether it transfers to this role — not by surface keyword overlap or domain. Anchors: 90–100 directly demonstrates a core must-have requirement of THIS posting; 70–89 strong evidence of a key skill it emphasizes; 50–69 clearly related or transferable; 30–49 tangential; 0–29 irrelevant. Return ONLY JSON: {"score": <int 0-100>}.`

// scorerUserPrompt frames one (posting, bullet) pair, matching the calibrated
// "A_clean" user template.
func scorerUserPrompt(jobText, bulletText string) string {
	return "=== JOB POSTING ===\n" + strings.TrimSpace(jobText) +
		"\n\n=== RÉSUMÉ BULLET ===\n" + strings.TrimSpace(bulletText) +
		"\n\nScore this bullet's relevance to the posting."
}

// BulletScore is one bullet's relevance score for a posting.
type BulletScore struct {
	RoleID   string `json:"role_id"`
	BulletID string `json:"bullet_id"`
	Score    int    `json:"score"`
}

// ScoreBullets scores every bullet against the posting in isolation, in
// parallel (bounded), and returns scores in the same order as the input. A
// per-bullet failure is non-fatal: that bullet scores 0 (it sinks to the bottom
// of its role and gets trimmed first) rather than failing the whole draft.
func (c *Client) ScoreBullets(ctx context.Context, jobText string, bullets []resume.Bullet) ([]BulletScore, error) {
	out := make([]BulletScore, len(bullets))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for i, b := range bullets {
		g.Go(func() error {
			s, err := c.scoreOne(ctx, jobText, b.Text)
			if err != nil {
				s = 0 // demote on failure rather than abort the batch
			}
			out[i] = BulletScore{RoleID: b.RoleID, BulletID: b.BulletID, Score: s}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// scoreOne sends a single (posting, bullet) pair to flash and parses the score.
func (c *Client) scoreOne(ctx context.Context, jobText, bulletText string) (int, error) {
	reqBody := chatRequest{
		Model: scorerModel(),
		Messages: []chatMessage{
			{Role: "system", Content: scorerSystemPrompt},
			{Role: "user", Content: scorerUserPrompt(jobText, bulletText)},
		},
		ResponseFormat: &responseFormat{Type: "json_object"},
		Temperature:    0, // deterministic scoring
	}
	raw, err := c.post(ctx, "/chat/completions", reqBody)
	if err != nil {
		return 0, err
	}
	var resp chatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0, fmt.Errorf("decode score response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return 0, fmt.Errorf("empty choices in score response")
	}
	var parsed struct {
		Score int `json:"score"`
	}
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &parsed); err != nil {
		return 0, fmt.Errorf("decode score JSON: %w", err)
	}
	if parsed.Score < 0 {
		parsed.Score = 0
	}
	if parsed.Score > 100 {
		parsed.Score = 100
	}
	return parsed.Score, nil
}
