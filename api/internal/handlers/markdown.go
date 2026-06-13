package handlers

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
)

// tailoredBullets applies a draft's removals and rewrites to the ordered base
// bullet slice: removed bullets are dropped, rewritten bullets take their new
// text, order is preserved. The result is rendered to markdown for the diff's
// right-hand side. Kept out of the resume package to avoid an import cycle
// (deepseek already imports resume).
func tailoredBullets(base []resume.Bullet, removed map[string]string, rewritten map[string]deepseek.Rewrite) []resume.Bullet {
	out := make([]resume.Bullet, 0, len(base))
	for _, b := range base {
		cid := b.CompositeID()
		if _, drop := removed[cid]; drop {
			continue
		}
		if rw, ok := rewritten[cid]; ok && rw.NewText != "" {
			b.Text = rw.NewText
		}
		out = append(out, b)
	}
	return out
}

// diffMarkdownHTML renders a line-level diff of two markdown documents as the
// stacked +/− HTML the draft pane shows, reusing the .diff-add/.diff-del/
// .diff-ctx classes already in the stylesheet.
func diffMarkdownHTML(base, tailored string) template.HTML {
	dmp := diffmatchpatch.New()
	a, b, lines := dmp.DiffLinesToChars(base, tailored)
	diffs := dmp.DiffCharsToLines(dmp.DiffMain(a, b, false), lines)

	var sb strings.Builder
	for _, d := range diffs {
		text := strings.TrimSuffix(d.Text, "\n")
		for _, ln := range strings.Split(text, "\n") {
			esc := template.HTMLEscapeString(ln)
			switch d.Type {
			case diffmatchpatch.DiffInsert:
				fmt.Fprintf(&sb, `<div class="diff-line diff-add">+ %s</div>`, esc)
			case diffmatchpatch.DiffDelete:
				fmt.Fprintf(&sb, `<div class="diff-line diff-del">− %s</div>`, esc)
			default:
				fmt.Fprintf(&sb, `<div class="diff-line diff-ctx">%s</div>`, esc)
			}
		}
	}
	return template.HTML(sb.String())
}
