package handlers

import (
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
