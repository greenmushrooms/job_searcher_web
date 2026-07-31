package deepseek

import (
	"strings"
	"testing"
)

// A generation is only useful if it ships tests to read AND code to write. The
// validator is the only thing standing between a lazy model reply and a wasted
// evening, so these cases are the contract.
func TestValidateChallenge(t *testing.T) {
	// Valid under ch-v3: two implementation modules, 40+ non-blank impl lines,
	// five tests, and no comment giving the planted fault away.
	fiveTests := "\ndef test_a():\n    pass\n\ndef test_b():\n    pass\n" +
		"\ndef test_c():\n    pass\n\ndef test_d():\n    pass\n\ndef test_e():\n    pass\n"
	good := func() *ChallengeResult {
		return &ChallengeResult{
			Title:   "Flatten nested survey responses",
			Minutes: 30,
			Brief:   "Build the flattener.",
			Files: []ChallengeFile{
				{Path: "flatten.py", Content: "def flatten(d):\n    return d\n" + strings.Repeat("x = 1\n", 25)},
				{Path: "aggregate.py", Content: "def agg(rows):\n    raise NotImplementedError\n" + strings.Repeat("y = 2\n", 20)},
				{Path: "test_flatten.py", Content: fiveTests},
			},
		}
	}

	t.Run("accepts a well-formed exercise", func(t *testing.T) {
		if err := validateChallenge(good()); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("defaults a missing time budget", func(t *testing.T) {
		ch := good()
		ch.Minutes = 0
		if err := validateChallenge(ch); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		if ch.Minutes != 30 {
			t.Fatalf("want 30 minutes, got %d", ch.Minutes)
		}
	})

	t.Run("rejects an exercise over the time budget", func(t *testing.T) {
		ch := good()
		ch.Minutes = MaxChallengeMinutes + 1
		if err := validateChallenge(ch); err == nil {
			t.Fatal("want error for an over-budget exercise, got nil")
		}
	})

	t.Run("rejects a suite with no tests", func(t *testing.T) {
		ch := good()
		ch.Files = ch.Files[:2] // implementation only
		if err := validateChallenge(ch); err == nil {
			t.Fatal("want error when no pytest file ships, got nil")
		}
	})

	t.Run("rejects tests with nothing to implement", func(t *testing.T) {
		ch := good()
		ch.Files = []ChallengeFile{{Path: "test_only.py", Content: fiveTests}}
		if err := validateChallenge(ch); err == nil {
			t.Fatal("want error when no implementation file ships, got nil")
		}
	})

	t.Run("accepts _test.py as well as test_ prefix", func(t *testing.T) {
		ch := good()
		ch.Files = []ChallengeFile{
			{Path: "pkg/flatten.py", Content: strings.Repeat("x = 1\n", 25)},
			{Path: "pkg/aggregate.py", Content: strings.Repeat("y = 2\n", 20)},
			{Path: "pkg/flatten_test.py", Content: fiveTests},
		}
		if err := validateChallenge(ch); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("rejects a traversal path anywhere in the file set", func(t *testing.T) {
		ch := good()
		ch.Files = append(ch.Files, ChallengeFile{Path: "../../.bashrc", Content: "evil"})
		if err := validateChallenge(ch); err == nil {
			t.Fatal("want error for traversal path, got nil")
		}
	})

	t.Run("rejects a traversal path in the solution too", func(t *testing.T) {
		ch := good()
		ch.Solution = []ChallengeFile{{Path: "/etc/cron.d/x", Content: "evil"}}
		if err := validateChallenge(ch); err == nil {
			t.Fatal("want error for absolute solution path, got nil")
		}
	})

	t.Run("rejects an empty title or brief", func(t *testing.T) {
		ch := good()
		ch.Title = "  "
		if err := validateChallenge(ch); err == nil {
			t.Fatal("want error for empty title, got nil")
		}
		ch = good()
		ch.Brief = ""
		if err := validateChallenge(ch); err == nil {
			t.Fatal("want error for empty brief, got nil")
		}
	})
}

// The generated paths get unzipped straight into the user's working tree, so
// anything that escapes the exercise directory has to be refused.
func TestValidateChallengePath(t *testing.T) {
	ok := []string{"flatten.py", "pkg/flatten.py", "a/b/c/test_x.py", "data/sample.json"}
	for _, p := range ok {
		if err := ValidateChallengePath(p); err != nil {
			t.Errorf("ValidateChallengePath(%q) = %v, want nil", p, err)
		}
	}

	bad := []string{
		"",
		"   ",
		"/etc/passwd",
		"../escape.py",
		"pkg/../../escape.py",
		`windows\path.py`,
		"http://example.com/x.py",
		"nul\x00byte.py",
	}
	for _, p := range bad {
		if err := ValidateChallengePath(p); err == nil {
			t.Errorf("ValidateChallengePath(%q) = nil, want error", p)
		}
	}
}

// Both ch-v1 and ch-v2 shipped a comment naming the planted fault, which turns
// the exercise into a grep. These are the actual lines they emitted.
func TestRejectsAnnotatedBug(t *testing.T) {
	base := func(loader string) *ChallengeResult {
		return &ChallengeResult{
			Title: "Survey pipeline", Minutes: 30, Brief: "Fix it.",
			Files: []ChallengeFile{
				{Path: "data_loader.py", Content: loader},
				{Path: "transformer.py", Content: strings.Repeat("x = 1\n", 30) +
					"def f():\n    raise NotImplementedError  # TODO: implement\n"},
				{Path: "test_pipeline.py", Content: "\ndef test_a():\n    pass\n\ndef test_b():\n    pass\n" +
					"\ndef test_c():\n    pass\n\ndef test_d():\n    pass\n\ndef test_e():\n    pass\n"},
			},
		}
	}

	annotated := []string{
		"def load():\n    # BUG: off-by-one, should return all\n    return RAW[:-1]\n",
		"def parse():\n    # BUG: inverted condition\n    return None\n",
		"def load():\n    # FIXME: this is broken\n    return None\n",
		"def load():\n    # intentionally wrong here\n    return None\n",
		"def load():\n    return RAW[:-1]  # should be RAW\n",
	}
	for _, src := range annotated {
		ch := base(src + strings.Repeat("y = 2\n", 20))
		if err := validateChallenge(ch); err == nil {
			t.Errorf("accepted an annotated bug:\n%s", src)
		}
	}

	t.Run("a clean planted bug passes", func(t *testing.T) {
		clean := "def load():\n    return RAW[:-1]\n" + strings.Repeat("y = 2\n", 20)
		if err := validateChallenge(base(clean)); err != nil {
			t.Errorf("rejected a clean exercise: %v", err)
		}
	})

	t.Run("debug/debugging must not trip the marker", func(t *testing.T) {
		ok := "import logging\n\ndef load():\n    logging.debug('loading')  # debug tracing for the pipeline\n    return RAW\n" +
			strings.Repeat("y = 2\n", 20)
		if err := validateChallenge(base(ok)); err != nil {
			t.Errorf("word-boundary failure — 'debug' tripped the bug marker: %v", err)
		}
	})

	t.Run("tests may say 'should return'", func(t *testing.T) {
		ch := base("def load():\n    return RAW[:-1]\n" + strings.Repeat("y = 2\n", 20))
		ch.Files[2].Content += "\ndef test_f():\n    # should return 5 rows\n    pass\n"
		if err := validateChallenge(ch); err != nil {
			t.Errorf("rejected a legitimate comment in a test file: %v", err)
		}
	})
}

// ch-v2 shipped 13 implementation lines and called itself a 30-minute exercise.
func TestRejectsTrivialExercise(t *testing.T) {
	tiny := &ChallengeResult{
		Title: "Tiny", Minutes: 30, Brief: "Fix it.",
		Files: []ChallengeFile{
			{Path: "data_loader.py", Content: "RAW = [1, 2, 3]\n\ndef load():\n    return RAW[:-1]\n"},
			{Path: "transformer.py", Content: "def f():\n    raise NotImplementedError\n"},
			{Path: "test_pipeline.py", Content: "\ndef test_a():\n    pass\n\ndef test_b():\n    pass\n\ndef test_c():\n    pass\n"},
		},
	}
	err := validateChallenge(tiny)
	if err == nil {
		t.Fatal("accepted a 13-line exercise claiming 30 minutes")
	}
	if !strings.Contains(err.Error(), "implementation lines") && !strings.Contains(err.Error(), "test functions") {
		t.Errorf("unexpected rejection reason: %v", err)
	}

	t.Run("one implementation module leaves the fault nowhere to hide", func(t *testing.T) {
		one := &ChallengeResult{
			Title: "One", Minutes: 30, Brief: "Fix it.",
			Files: []ChallengeFile{
				{Path: "only.py", Content: strings.Repeat("x = 1\n", 50)},
				{Path: "test_only.py", Content: "\ndef test_a():\n    pass\n\ndef test_b():\n    pass\n\ndef test_c():\n    pass\n\ndef test_d():\n    pass\n\ndef test_e():\n    pass\n"},
			},
		}
		if err := validateChallenge(one); err == nil {
			t.Error("accepted a single-module exercise")
		}
	})
}
