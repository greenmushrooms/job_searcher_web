package attempts

import (
	"strings"
	"testing"
	"time"
)

func mkRun(min int, passed, failed []string) Run {
	return Run{
		TS:     time.Date(2026, 7, 31, 10, min, 0, 0, time.UTC),
		Passed: passed,
		Failed: failed,
		Total:  len(passed) + len(failed),
	}
}

func TestParseLog(t *testing.T) {
	t.Run("reads well-formed lines in order", func(t *testing.T) {
		in := `{"ts":"2026-07-31T10:00:00Z","passed":["test_a"],"failed":["test_b"],"total":2}
{"ts":"2026-07-31T10:05:00Z","passed":["test_a","test_b"],"failed":[],"total":2}`
		runs, err := ParseLog(strings.NewReader(in))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(runs) != 2 {
			t.Fatalf("want 2 runs, got %d", len(runs))
		}
		if !runs[0].TS.Before(runs[1].TS) {
			t.Error("runs are not in chronological order")
		}
	})

	t.Run("skips a truncated final line rather than losing the history", func(t *testing.T) {
		in := `{"ts":"2026-07-31T10:00:00Z","passed":["test_a"],"failed":[],"total":1}
{"ts":"2026-07-31T10:05:00Z","passed":["test_`
		runs, err := ParseLog(strings.NewReader(in))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(runs) != 1 {
			t.Fatalf("want 1 usable run, got %d", len(runs))
		}
	})

	t.Run("skips empty runs where nothing was collected", func(t *testing.T) {
		in := `{"ts":"2026-07-31T10:00:00Z","passed":[],"failed":[],"total":0}
{"ts":"2026-07-31T10:05:00Z","passed":["test_a"],"failed":[],"total":1}`
		runs, err := ParseLog(strings.NewReader(in))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(runs) != 1 {
			t.Fatalf("want 1 run, got %d", len(runs))
		}
	})

	t.Run("errors when there is nothing usable at all", func(t *testing.T) {
		if _, err := ParseLog(strings.NewReader("garbage\n\n")); err == nil {
			t.Fatal("want an error for a log with no usable runs")
		}
	})
}

func TestScoreBasics(t *testing.T) {
	runs := []Run{
		mkRun(0, []string{"test_a"}, []string{"test_b", "test_c"}),
		mkRun(12, []string{"test_a", "test_b", "test_c"}, nil),
	}
	s := Score(runs, nil)

	if s.Runs != 2 {
		t.Errorf("Runs = %d, want 2", s.Runs)
	}
	if s.FirstPass != 1 || s.FinalPass != 3 {
		t.Errorf("FirstPass/FinalPass = %d/%d, want 1/3", s.FirstPass, s.FinalPass)
	}
	if s.TotalTests != 3 {
		t.Errorf("TotalTests = %d, want 3", s.TotalTests)
	}
	if !s.Solved {
		t.Error("Solved = false, want true")
	}
	if got := s.Minutes(); got != 12 {
		t.Errorf("Minutes = %v, want 12", got)
	}
	if s.BugFirst != nil {
		t.Errorf("BugFirst = %v, want nil when no bug tests are known", *s.BugFirst)
	}
}

func TestScoreNeverGreen(t *testing.T) {
	runs := []Run{
		mkRun(0, []string{"test_a"}, []string{"test_b"}),
		mkRun(20, []string{"test_a"}, []string{"test_b"}),
	}
	s := Score(runs, nil)
	if s.Solved {
		t.Error("Solved = true, want false")
	}
	if s.FinishedAt != nil {
		t.Error("FinishedAt set on an unsolved attempt")
	}
	if s.Minutes() != 0 {
		t.Errorf("Minutes = %v, want 0 when never green", s.Minutes())
	}
}

// The whole point of the feature: did they localise the planted bug before
// doing the obvious stub work? Ordering is derived, never self-reported.
func TestScoreBugOrdering(t *testing.T) {
	bug := []string{"test_parse_valid", "test_parse_range"}

	t.Run("bug fixed first", func(t *testing.T) {
		runs := []Run{
			mkRun(0, nil, []string{"test_parse_valid", "test_parse_range", "test_avg_one", "test_avg_many"}),
			// parser tests go green while the aggregator stubs still fail
			mkRun(6, []string{"test_parse_valid", "test_parse_range"}, []string{"test_avg_one", "test_avg_many"}),
			mkRun(15, []string{"test_parse_valid", "test_parse_range", "test_avg_one", "test_avg_many"}, nil),
		}
		s := Score(runs, bug)
		if s.BugGreenRun != 2 {
			t.Errorf("BugGreenRun = %d, want 2", s.BugGreenRun)
		}
		if s.OtherGreenRun != 3 {
			t.Errorf("OtherGreenRun = %d, want 3", s.OtherGreenRun)
		}
		if s.BugFirst == nil || !*s.BugFirst {
			t.Error("BugFirst should be true when the planted bug went green first")
		}
	})

	t.Run("chased the loud stub first — the Perceptyx trap", func(t *testing.T) {
		runs := []Run{
			mkRun(0, nil, []string{"test_parse_valid", "test_parse_range", "test_avg_one", "test_avg_many"}),
			// aggregator implemented, parser bug still lurking
			mkRun(8, []string{"test_avg_one", "test_avg_many"}, []string{"test_parse_valid", "test_parse_range"}),
			mkRun(22, []string{"test_parse_valid", "test_parse_range", "test_avg_one", "test_avg_many"}, nil),
		}
		s := Score(runs, bug)
		if s.OtherGreenRun != 2 {
			t.Errorf("OtherGreenRun = %d, want 2", s.OtherGreenRun)
		}
		if s.BugGreenRun != 3 {
			t.Errorf("BugGreenRun = %d, want 3", s.BugGreenRun)
		}
		if s.BugFirst == nil || *s.BugFirst {
			t.Error("BugFirst should be false when the stub work went first")
		}
	})

	t.Run("everything green in one go is a tie, not a win", func(t *testing.T) {
		runs := []Run{
			mkRun(0, nil, []string{"test_parse_valid", "test_parse_range", "test_avg_one"}),
			mkRun(9, []string{"test_parse_valid", "test_parse_range", "test_avg_one"}, nil),
		}
		s := Score(runs, bug)
		if s.BugFirst != nil {
			t.Errorf("BugFirst = %v, want nil when both went green on the same run", *s.BugFirst)
		}
	})

	t.Run("matches bare names against full pytest nodeids", func(t *testing.T) {
		runs := []Run{
			mkRun(0, nil, []string{"test_pipeline.py::test_parse_valid", "test_pipeline.py::test_avg_one"}),
			mkRun(5, []string{"test_pipeline.py::test_parse_valid"}, []string{"test_pipeline.py::test_avg_one"}),
			mkRun(10, []string{"test_pipeline.py::test_parse_valid", "test_pipeline.py::test_avg_one"}, nil),
		}
		s := Score(runs, []string{"test_parse_valid"})
		if s.BugGreenRun != 2 {
			t.Errorf("BugGreenRun = %d, want 2 — bare names must match nodeids", s.BugGreenRun)
		}
		if s.BugFirst == nil || !*s.BugFirst {
			t.Error("BugFirst should be true")
		}
	})
}

func TestBareName(t *testing.T) {
	cases := map[string]string{
		"test_x":                                 "test_x",
		"test_pipeline.py::test_x":               "test_x",
		"pkg/test_pipeline.py::TestCls::test_x":  "test_x",
		"test_pipeline.py::test_x[param-1]":      "test_x",
		"pkg/test_p.py::TestCls::test_x[a-b][c]": "test_x",
	}
	for in, want := range cases {
		if got := bareName(in); got != want {
			t.Errorf("bareName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMedian(t *testing.T) {
	if got := median(nil); got != 0 {
		t.Errorf("median(nil) = %v, want 0", got)
	}
	if got := median([]float64{5}); got != 5 {
		t.Errorf("median([5]) = %v, want 5", got)
	}
	if got := median([]float64{9, 1, 5}); got != 5 {
		t.Errorf("median([9,1,5]) = %v, want 5", got)
	}
	if got := median([]float64{4, 1, 9, 3}); got != 3.5 {
		t.Errorf("median([4,1,9,3]) = %v, want 3.5", got)
	}
}

// The conftest is shipped as source into the user's tree, so it must at least
// be syntactically plausible Python with the hooks pytest actually calls.
func TestConftestTemplate(t *testing.T) {
	for _, want := range []string{
		"def pytest_runtest_logreport(report):",
		"def pytest_sessionfinish(session, exitstatus):",
		AttemptLogName,
		`"a", encoding="utf-8"`, // appends, never truncates
		"except OSError:",       // telemetry must not break the run
	} {
		if !strings.Contains(ConftestTemplate, want) {
			t.Errorf("conftest missing %q", want)
		}
	}
}
