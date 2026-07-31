// Package attempts turns the run log a practice exercise writes on disk into a
// scored attempt. The candidate never self-reports: they run `pytest` as usual,
// a conftest.py shipped inside the exercise appends one JSON line per run, and
// uploading that file is the whole interaction.
//
// The interesting signal is ORDER. Because each planted-bug test is known at
// generation time, the run at which those tests go green can be compared to the
// run at which everything else does — which says whether the subtle fault was
// localised before or after the obvious stub work. That is the habit these
// exercises exist to drill, measured without asking.
package attempts

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// ConftestTemplate is written into every generated exercise. Pure standard
// library, no pytest plugins, and it appends rather than truncates so a run log
// survives across sittings. Failure to write must never break the test run —
// the exercise matters more than the telemetry.
const ConftestTemplate = `# Records one JSON line per pytest run into .attempts.jsonl, so your practice
# history can be uploaded and scored. Pure stdlib, appends only, and never
# interferes with the run itself. Delete this file if you'd rather not log.
import json
import pathlib
from datetime import datetime, timezone

_LOG = pathlib.Path(__file__).parent / ".attempts.jsonl"
_results = {}


def pytest_runtest_logreport(report):
    if report.when == "call":
        _results[report.nodeid] = report.outcome
    elif report.when == "setup" and report.outcome in ("failed", "error"):
        _results[report.nodeid] = "error"


def pytest_sessionfinish(session, exitstatus):
    passed = sorted(n for n, o in _results.items() if o == "passed")
    failed = sorted(n for n, o in _results.items() if o != "passed")
    record = {
        "ts": datetime.now(timezone.utc).isoformat(),
        "passed": passed,
        "failed": failed,
        "total": len(_results),
    }
    try:
        with _LOG.open("a", encoding="utf-8") as fh:
            fh.write(json.dumps(record) + "\n")
    except OSError:
        pass  # telemetry must never break the exercise
`

// AttemptLogName is the file the conftest writes and the UI asks for.
const AttemptLogName = ".attempts.jsonl"

// Run is one pytest invocation as recorded by the conftest.
type Run struct {
	TS     time.Time `json:"ts"`
	Passed []string  `json:"passed"`
	Failed []string  `json:"failed"`
	Total  int       `json:"total"`
}

// Scored is the analysis of a whole run log.
type Scored struct {
	Runs          int
	StartedAt     time.Time
	FinishedAt    *time.Time // first run with nothing failing
	TotalTests    int
	FirstPass     int
	FinalPass     int
	Solved        bool
	BugGreenRun   int   // 1-based run where every planted-bug test first passed
	OtherGreenRun int   // 1-based run where every other test first passed
	BugFirst      *bool // nil when unknown or when both went green together
	Detail        []Run
}

// Minutes is the wall time from the first run to the run that went green.
// Zero when the attempt never went green.
func (s *Scored) Minutes() float64 {
	if s.FinishedAt == nil {
		return 0
	}
	return s.FinishedAt.Sub(s.StartedAt).Minutes()
}

// ParseLog reads the .attempts.jsonl the conftest produced. Malformed lines are
// skipped rather than fatal: a half-flushed final line shouldn't cost the whole
// history. Runs are returned in file order, which is chronological by append.
func ParseLog(r io.Reader) ([]Run, error) {
	var runs []Run
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // one run's nodeids can be long
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var run Run
		if err := json.Unmarshal([]byte(line), &run); err != nil {
			continue
		}
		if run.Total == 0 && len(run.Passed) == 0 && len(run.Failed) == 0 {
			continue // nothing collected — an import error or an empty run
		}
		runs = append(runs, run)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read attempt log: %w", err)
	}
	if len(runs) == 0 {
		return nil, fmt.Errorf("no usable pytest runs in the log")
	}
	return runs, nil
}

// Score analyses a run log. bugTests are the bare test names the planted bug
// breaks; pass nil when the exercise predates scoring data, and the ordering
// signal is simply left unknown.
func Score(runs []Run, bugTests []string) *Scored {
	out := &Scored{
		Runs:      len(runs),
		StartedAt: runs[0].TS,
		Detail:    runs,
		FirstPass: len(runs[0].Passed),
		FinalPass: len(runs[len(runs)-1].Passed),
	}
	for _, r := range runs {
		if n := r.Total; n > out.TotalTests {
			out.TotalTests = n
		}
	}
	for i, r := range runs {
		if len(r.Failed) == 0 && r.Total > 0 {
			out.Solved = true
			ts := runs[i].TS
			out.FinishedAt = &ts
			break
		}
	}

	bug := normalize(bugTests)
	if len(bug) == 0 {
		return out
	}
	for i, r := range runs {
		passed := passedSet(r)
		if out.BugGreenRun == 0 && coversAll(passed, bug) {
			out.BugGreenRun = i + 1
		}
		// "Everything else" means every test seen in this log that isn't a
		// planted-bug test — computed per run so a growing suite can't skew it.
		others := otherTests(r, bug)
		if out.OtherGreenRun == 0 && len(others) > 0 && coversAll(passed, others) {
			out.OtherGreenRun = i + 1
		}
	}
	if out.BugGreenRun > 0 && out.OtherGreenRun > 0 && out.BugGreenRun != out.OtherGreenRun {
		first := out.BugGreenRun < out.OtherGreenRun
		out.BugFirst = &first
	}
	return out
}

// passedSet indexes a run's passing tests by both full nodeid and bare name, so
// a caller's bare "test_parse_score" matches "test_pipeline.py::test_parse_score".
func passedSet(r Run) map[string]bool {
	set := make(map[string]bool, len(r.Passed)*2)
	for _, n := range r.Passed {
		set[n] = true
		set[bareName(n)] = true
	}
	return set
}

// otherTests returns the bare names of every test in this run that is not a
// planted-bug test.
func otherTests(r Run, bug map[string]bool) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, n := range append(append([]string{}, r.Passed...), r.Failed...) {
		name := bareName(n)
		if bug[name] || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func coversAll(passed map[string]bool, want any) bool {
	switch w := want.(type) {
	case map[string]bool:
		for name := range w {
			if !passed[name] {
				return false
			}
		}
		return len(w) > 0
	case []string:
		for _, name := range w {
			if !passed[name] {
				return false
			}
		}
		return len(w) > 0
	}
	return false
}

func normalize(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		if n = bareName(strings.TrimSpace(n)); n != "" {
			out[n] = true
		}
	}
	return out
}

// bareName strips a pytest nodeid down to the test function name:
// "pkg/test_x.py::TestClass::test_thing[param]" -> "test_thing".
func bareName(nodeID string) string {
	s := nodeID
	if i := strings.LastIndex(s, "::"); i >= 0 {
		s = s[i+2:]
	}
	if i := strings.Index(s, "["); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
