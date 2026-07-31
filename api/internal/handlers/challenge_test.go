package handlers

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/greenmushrooms/job_searcher_web/api/internal/challenges"
	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
)

func TestChallengeSlug(t *testing.T) {
	cases := map[string]string{
		"Flatten nested survey responses": "flatten-nested-survey-responses",
		"  Spaces  &  Symbols!! ":         "spaces-symbols",
		"":                                "practice-challenge",
		"!!!":                             "practice-challenge",
		strings.Repeat("a", 100):          strings.Repeat("a", 60),
	}
	for in, want := range cases {
		if got := challengeSlug(in); got != want {
			t.Errorf("challengeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// The README carries the house rule that makes the drill worth doing — read
// the failing test before editing — and must never name the buggy module.
func TestChallengeReadme(t *testing.T) {
	ch := &challenges.Challenge{
		Title:   "Flatten nested survey responses",
		Minutes: 30,
		Skills:  []string{"Python", "pytest"},
		Brief:   "Build a flattener for nested response payloads.",
	}
	out := challengeReadme(ch)

	for _, want := range []string{
		"# Flatten nested survey responses",
		"**Time budget:** 30 minutes",
		"Python, pytest",
		"Build a flattener",
		"pytest",
		"read the failing test",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("README missing %q\n---\n%s", want, out)
		}
	}
}

// The zip is the deliverable: it must contain the README plus every exercise
// file under one directory, and must never contain the reference solution.
func TestChallengeZipContents(t *testing.T) {
	ch := &challenges.Challenge{
		Title:   "Flatten nested survey responses",
		Minutes: 30,
		Brief:   "Build it.",
		Files: []deepseek.ChallengeFile{
			{Path: "flatten.py", Content: "def flatten(d): ..."},
			{Path: "test_flatten.py", Content: "def test_flatten(): ..."},
			{Path: "../escape.py", Content: "should be skipped"},
		},
		Solution: []deepseek.ChallengeFile{
			{Path: "flatten.py", Content: "THE ANSWER"},
		},
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	root := challengeSlug(ch.Title)
	if err := writeZipEntry(zw, root+"/README.md", challengeReadme(ch)); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	for _, f := range ch.Files {
		if err := deepseek.ValidateChallengePath(f.Path); err != nil {
			continue
		}
		if err := writeZipEntry(zw, root+"/"+f.Path, f.Content); err != nil {
			t.Fatalf("write %s: %v", f.Path, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(b)
	}

	for _, want := range []string{
		root + "/README.md",
		root + "/flatten.py",
		root + "/test_flatten.py",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("zip missing %s (have %v)", want, keys(got))
		}
	}
	if len(got) != 3 {
		t.Errorf("zip has %d entries, want 3 (traversal path must be skipped): %v", len(got), keys(got))
	}
	for name, content := range got {
		if strings.Contains(content, "THE ANSWER") {
			t.Errorf("zip entry %s leaked the reference solution", name)
		}
		if strings.Contains(name, "..") {
			t.Errorf("zip entry %s escapes the exercise directory", name)
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
