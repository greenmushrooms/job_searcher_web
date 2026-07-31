package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ChallengePromptVersion is logged into every challenge_drafted event, same
// retro contract as PromptVersion for résumé drafts.
// ch-v1: first cut — a ~30-minute pure-stdlib pytest exercise built from the
// posting's stated must-haves, shipped as a failing suite with a bug planted
// in one module so the first task is localising the fault, not writing code.
// ch-v2: the model now names the buggy module and the tests that bug breaks,
// so an uploaded run log can be scored — did you localise the planted fault
// before doing the obvious stub work — without self-reporting. Never shown to
// the candidate; scoring data only.
const ChallengePromptVersion = "ch-v2"

// MaxChallengeMinutes caps what the model may ask for. The whole point is a
// drill that survives a weeknight, so an "exercise" the model sizes at two
// hours is a failed generation, not a hard one.
const MaxChallengeMinutes = 45

// challengeSystemPrompt frames the generation task. JSON out, because the
// reply is a file set rather than prose.
const challengeSystemPrompt = `You design short technical practice exercises that rehearse a candidate for a SPECIFIC job's technical round.

Read the posting and identify the technical requirements it states explicitly — the skills the employer said they will screen for. Build ONE small Python exercise that drills the most testable of them.

Hard requirements:
- Python 3.11+, standard library ONLY, plus pytest. Never import pandas, numpy, requests, or anything else unless the posting itself names that library as a core requirement.
- The whole exercise must be completable in 30 minutes by a competent engineer. Small and sharp beats broad.
- Ship a FAILING pytest suite. The tests are the specification: reading them is how the candidate learns what to build.
- Plant exactly ONE deliberate bug in a module that is otherwise already written, and do NOT say which module in the brief. The candidate's first job is to read the failing test output and localise the fault. Make the bug plausible — an off-by-one, a wrong accumulator, a mutated shared default, a swapped branch — never a syntax error or an obvious placeholder.
- Include at least one module that is genuinely incomplete (raises NotImplementedError or returns a wrong stub), so there is real code to write as well as a bug to find.
- Tests must be deterministic. No randomness, no clocks, no network, no filesystem writes outside tmp_path.
- Every file must be runnable as written: 'pytest' in the exercise directory is the only command needed.

Output STRICT JSON, no markdown fence, matching exactly:
{
  "title": "short exercise name",
  "minutes": 30,
  "skills": ["the posting requirements this drills"],
  "brief": "markdown: the scenario, what to build, how to run the tests, and the rule that they must read the failing test before editing. Never name the module holding the planted bug.",
  "files": [{"path": "relative/path.py", "content": "file contents"}],
  "solution": [{"path": "relative/path.py", "content": "reference implementation of the same path"}],
  "bug_module": "path of the file holding the planted bug",
  "bug_tests": ["names of the tests that fail BECAUSE of the planted bug, not because of the unimplemented stub"]
}

"files" is what the candidate gets: the stub modules, the buggy module, and the test suite. "solution" is the corrected version of every non-test file — it must make the shipped test suite pass without editing the tests.

"bug_module" and "bug_tests" are scoring data. They are never shown to the candidate. Split the failing tests honestly: a test that fails with NotImplementedError from the stub is NOT a bug_test; a test that fails because the already-written module computes the wrong answer IS. Use bare test function names.`

// ChallengeFile is one generated file: a path relative to the exercise root
// and its full contents.
type ChallengeFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ChallengeResult is one generated exercise plus the usual cost telemetry.
type ChallengeResult struct {
	Title         string          `json:"title"`
	Minutes       int             `json:"minutes"`
	Skills        []string        `json:"skills"`
	Brief         string          `json:"brief"`
	Files         []ChallengeFile `json:"files"`
	Solution      []ChallengeFile `json:"solution"`
	BugModule     string          `json:"bug_module"` // scoring only — never rendered
	BugTests      []string        `json:"bug_tests"`  // tests the planted bug breaks
	Usage         Usage           `json:"usage"`
	Model         string          `json:"model"`
	PromptVersion string          `json:"prompt_version"`
}

// Challenge generates a practice exercise from a job posting. The candidate's
// résumé is deliberately NOT sent: the exercise should rehearse what the
// employer asked for, not flatter what the candidate already does.
func (c *Client) Challenge(ctx context.Context, title, company, jobDescription string) (*ChallengeResult, error) {
	reqBody := chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: challengeSystemPrompt},
			{Role: "user", Content: buildChallengePrompt(title, company, jobDescription)},
		},
		ResponseFormat: &responseFormat{Type: "json_object"},
		Temperature:    0.4, // some variety across re-rolls, but stable structure
		// Thinking left at the model default: unlike the résumé diff, getting a
		// coherent bug + matching test suite is exactly the kind of task the
		// chain-of-thought pays for.
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

	var out ChallengeResult
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, fmt.Errorf("decode challenge JSON: %w (content=%s)", err, truncate(content, 500))
	}
	if err := validateChallenge(&out); err != nil {
		return nil, err
	}

	out.Usage = Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
		CostUSD:          estimateCost(c.Model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens),
	}
	out.Model = c.Model
	out.PromptVersion = ChallengePromptVersion
	return &out, nil
}

// validateChallenge rejects generations that would waste the candidate's
// evening: no tests to read, no code to write, paths that escape the exercise
// directory, or an exercise the model itself sized past the time budget.
func validateChallenge(ch *ChallengeResult) error {
	if strings.TrimSpace(ch.Title) == "" {
		return fmt.Errorf("challenge has no title")
	}
	if strings.TrimSpace(ch.Brief) == "" {
		return fmt.Errorf("challenge has no brief")
	}
	if len(ch.Files) == 0 {
		return fmt.Errorf("challenge has no files")
	}
	if ch.Minutes <= 0 {
		ch.Minutes = 30
	}
	if ch.Minutes > MaxChallengeMinutes {
		return fmt.Errorf("challenge is %d minutes, over the %d-minute budget", ch.Minutes, MaxChallengeMinutes)
	}

	tests, impl := 0, 0
	for _, f := range append(append([]ChallengeFile{}, ch.Files...), ch.Solution...) {
		if err := ValidateChallengePath(f.Path); err != nil {
			return err
		}
	}
	for _, f := range ch.Files {
		base := f.Path
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		switch {
		case strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py"):
			tests++
		case strings.HasSuffix(base, ".py"):
			impl++
		}
	}
	if tests == 0 {
		return fmt.Errorf("challenge ships no pytest file — the tests are the spec")
	}
	if impl == 0 {
		return fmt.Errorf("challenge ships no implementation file to work on")
	}
	normalizeScoring(ch)
	return nil
}

// normalizeScoring drops bug_module/bug_tests that don't correspond to the
// generated files. Deliberately lenient: the exercise is the product and the
// scoring data is a bonus, so bad metadata makes an attempt unscoreable rather
// than throwing away a perfectly good exercise.
func normalizeScoring(ch *ChallengeResult) {
	paths := make(map[string]bool, len(ch.Files))
	var testSrc strings.Builder
	for _, f := range ch.Files {
		paths[f.Path] = true
		base := f.Path
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
			testSrc.WriteString(f.Content)
		}
	}
	if !paths[ch.BugModule] {
		ch.BugModule = ""
	}
	// A named test we can't find in any test file would silently never go
	// green, which would score every attempt as a failure to localise.
	src := testSrc.String()
	kept := make([]string, 0, len(ch.BugTests))
	for _, name := range ch.BugTests {
		if n := strings.TrimSpace(name); n != "" && strings.Contains(src, n) {
			kept = append(kept, n)
		}
	}
	ch.BugTests = kept
	if ch.BugModule == "" || len(ch.BugTests) == 0 {
		// Partial scoring data is worse than none — it reads as authoritative.
		ch.BugModule, ch.BugTests = "", nil
	}
}

// ValidateChallengePath keeps generated paths inside the exercise directory.
// The files get unzipped into the user's own working tree, so a model that
// emits "../../.bashrc" or "/etc/cron.d/x" must never reach a zip entry.
// Exported because the zip writer re-checks rows that predate this validation.
func ValidateChallengePath(p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("challenge file has an empty path")
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return fmt.Errorf("challenge file path %q is absolute", p)
	}
	if strings.Contains(p, `\`) {
		return fmt.Errorf("challenge file path %q uses backslashes", p)
	}
	if strings.Contains(p, "://") || strings.Contains(p, "\x00") {
		return fmt.Errorf("challenge file path %q is not a plain relative path", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("challenge file path %q escapes the exercise directory", p)
		}
	}
	return nil
}

// buildChallengePrompt assembles the user message: just the posting. What the
// employer said they screen for is the whole input.
func buildChallengePrompt(title, company, jobDescription string) string {
	var b strings.Builder
	b.WriteString("=== JOB ===\n")
	b.WriteString("Title: ")
	b.WriteString(strings.TrimSpace(title))
	b.WriteString("\nCompany: ")
	b.WriteString(strings.TrimSpace(company))
	b.WriteString("\n\n=== JOB DESCRIPTION ===\n")
	b.WriteString(strings.TrimSpace(jobDescription))
	b.WriteString("\n\nDesign the exercise now.")
	return b.String()
}
