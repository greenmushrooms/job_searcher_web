package deepseek

import "testing"

// A generation is only useful if it ships tests to read AND code to write. The
// validator is the only thing standing between a lazy model reply and a wasted
// evening, so these cases are the contract.
func TestValidateChallenge(t *testing.T) {
	good := func() *ChallengeResult {
		return &ChallengeResult{
			Title:   "Flatten nested survey responses",
			Minutes: 30,
			Brief:   "Build the flattener.",
			Files: []ChallengeFile{
				{Path: "flatten.py", Content: "def flatten(d): raise NotImplementedError"},
				{Path: "test_flatten.py", Content: "def test_flatten(): assert False"},
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
		ch.Files = ch.Files[:1] // implementation only
		if err := validateChallenge(ch); err == nil {
			t.Fatal("want error when no pytest file ships, got nil")
		}
	})

	t.Run("rejects tests with nothing to implement", func(t *testing.T) {
		ch := good()
		ch.Files = []ChallengeFile{{Path: "test_only.py", Content: "def test_x(): pass"}}
		if err := validateChallenge(ch); err == nil {
			t.Fatal("want error when no implementation file ships, got nil")
		}
	})

	t.Run("accepts _test.py as well as test_ prefix", func(t *testing.T) {
		ch := good()
		ch.Files = []ChallengeFile{
			{Path: "pkg/flatten.py", Content: "x"},
			{Path: "pkg/flatten_test.py", Content: "y"},
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
