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

// docWithEducationPruned returns a shallow copy of doc with the education
// entries whose id is in `removed` filtered out, for the tailored (right-pane)
// render. The base doc is never mutated — the working copy keeps the full
// education list. Returns doc unchanged when nothing is removed.
func docWithEducationPruned(doc *resume.Document, removed map[string]string) *resume.Document {
	if doc == nil || len(removed) == 0 {
		return doc
	}
	out := *doc
	out.Education = make([]resume.DocEducation, 0, len(doc.Education))
	for _, e := range doc.Education {
		if _, drop := removed[e.ID]; drop {
			continue
		}
		out.Education = append(out.Education, e)
	}
	return &out
}
