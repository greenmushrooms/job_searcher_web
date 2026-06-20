// Package resumediff computes a two-column, line-aligned diff between two
// markdown résumés and renders it to escaped HTML for the zero-JS /v4 diff lab.
//
// The algorithm mirrors the client-side diff that web/resume_diff.js (the v2
// CodeMirror editor) runs in the browser: an LCS over exactly-equal lines gives
// anchors; each changed gap between anchors is aligned by line similarity
// (delete / add / substitute) so an unrelated deleted point is never word-diffed
// into a changed point next to it; and substituted line pairs get a word-level
// LCS so only the changed words are highlighted. Unlike the browser version this
// runs in Go (unit-testable) and emits already-aligned table rows instead of
// absolutely-positioned spacers — the panes render with white-space: pre-wrap in
// a fixed-layout table, so each row's two cells share a height automatically and
// no spacer math is needed.
package resumediff

import (
	"html/template"
	"sort"
	"strings"
	"unicode"
)

// Cell is one side of a rendered diff row.
type Cell struct {
	Class string        // "", "del", "add", "chg"
	HTML  template.HTML // escaped line content, with <span class="d"> around changed words
	Blank bool          // true => no line on this side (render a filler cell)
}

// RenderRow is one visual row: a left line, a right line, or one of each.
type RenderRow struct {
	Left  Cell
	Right Cell
}

// Render diffs the two markdown documents and returns aligned rows ready for the
// difflab_diff template.
func Render(aText, bText string) []RenderRow {
	A, B := splitLines(aText), splitLines(bText)
	rows := computeRows(A, B)
	out := make([]RenderRow, 0, len(rows))
	for _, rw := range rows {
		var rr RenderRow
		switch rw.kind {
		case "same":
			rr.Left = Cell{HTML: esc(A[rw.l])}
			rr.Right = Cell{HTML: esc(B[rw.r])}
		case "sub":
			al, bl := []rune(A[rw.l]), []rune(B[rw.r])
			am, bm := wordDiff(al, bl)
			rr.Left = Cell{Class: "chg", HTML: marked(al, am)}
			rr.Right = Cell{Class: "chg", HTML: marked(bl, bm)}
		case "replace":
			rr.Left = Cell{Class: "del", HTML: esc(A[rw.l])}
			rr.Right = Cell{Class: "add", HTML: esc(B[rw.r])}
		case "del":
			rr.Left = Cell{Class: "del", HTML: esc(A[rw.l])}
			rr.Right = Cell{Blank: true}
		case "add":
			rr.Left = Cell{Blank: true}
			rr.Right = Cell{Class: "add", HTML: esc(B[rw.r])}
		}
		out = append(out, rr)
	}
	return out
}

// ── line alignment ─────────────────────────────────────────────────────────

type row struct {
	l, r int    // line indices, -1 if absent on that side
	kind string // same | sub | replace | del | add
}

// computeRows builds the ordered diff rows: an LCS over exactly-equal lines
// yields anchors, and each gap between anchors is aligned by similarity.
func computeRows(A, B []string) []row {
	n, m := len(A), len(B)
	dp := lcsTable(n, m, func(i, j int) bool { return A[i] == B[j] })

	var matches [][2]int
	for i, j := 0, 0; i < n && j < m; {
		switch {
		case A[i] == B[j]:
			matches = append(matches, [2]int{i, j})
			i, j = i+1, j+1
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}

	var rows []row
	emitGap := func(loI, hiI, loJ, hiJ int) {
		var Lidx, Ridx []int
		for x := loI; x < hiI; x++ {
			Lidx = append(Lidx, x)
		}
		for y := loJ; y < hiJ; y++ {
			Ridx = append(Ridx, y)
		}
		if len(Lidx) == 0 && len(Ridx) == 0 {
			return
		}
		ops := alignGap(Lidx, Ridx, A, B)
		var bd, ba []int
		flush := func() {
			k := min(len(bd), len(ba))
			for t := 0; t < k; t++ {
				rows = append(rows, row{bd[t], ba[t], "replace"})
			}
			for t := k; t < len(bd); t++ {
				rows = append(rows, row{bd[t], -1, "del"})
			}
			for t := k; t < len(ba); t++ {
				rows = append(rows, row{-1, ba[t], "add"})
			}
			bd, ba = nil, nil
		}
		for _, o := range ops {
			switch o.kind {
			case 's':
				flush()
				rows = append(rows, row{o.a, o.b, "sub"})
			case 'd':
				bd = append(bd, o.a)
			default:
				ba = append(ba, o.b)
			}
		}
		flush()
	}

	pi, pj := -1, -1
	for _, mt := range matches {
		emitGap(pi+1, mt[0], pj+1, mt[1])
		rows = append(rows, row{mt[0], mt[1], "same"})
		pi, pj = mt[0], mt[1]
	}
	emitGap(pi+1, n, pj+1, m)
	return rows
}

type op struct {
	kind byte // 's' sub, 'd' del, 'a' add
	a, b int
}

// alignGap finds the best order-preserving del/add/substitute alignment of the
// changed lines in one gap. A substitution is only allowed between lines that
// are at least THRESH similar, so dissimilar points fall through to del+add
// (later merged into whole-line "replace" rows) instead of being word-diffed
// into each other.
func alignGap(Lidx, Ridx []int, A, B []string) []op {
	p, q := len(Lidx), len(Ridx)
	const thresh = 0.5
	const inf = 1e18
	cost := make([][]float64, p+1)
	back := make([][]byte, p+1)
	for i := range cost {
		cost[i] = make([]float64, q+1)
		back[i] = make([]byte, q+1)
	}
	for i := p; i >= 0; i-- {
		for j := q; j >= 0; j-- {
			if i == p && j == q {
				cost[i][j] = 0
				continue
			}
			best, bk := inf, byte(0)
			if i < p {
				if c := 1 + cost[i+1][j]; c < best {
					best, bk = c, 1
				}
			}
			if j < q {
				if c := 1 + cost[i][j+1]; c < best {
					best, bk = c, 2
				}
			}
			if i < p && j < q {
				if s := lineSim(A[Lidx[i]], B[Ridx[j]]); s >= thresh {
					if c := (1 - s) + cost[i+1][j+1]; c < best {
						best, bk = c, 3
					}
				}
			}
			cost[i][j], back[i][j] = best, bk
		}
	}
	var ops []op
	for i, j := 0, 0; i < p || j < q; {
		switch bk := back[i][j]; {
		case bk == 3:
			ops = append(ops, op{'s', Lidx[i], Ridx[j]})
			i, j = i+1, j+1
		case bk == 1 || (bk == 0 && i < p):
			ops = append(ops, op{'d', Lidx[i], -1})
			i++
		default:
			ops = append(ops, op{'a', -1, Ridx[j]})
			j++
		}
	}
	return ops
}

// lineSim is the Dice coefficient over the two lines' non-whitespace word
// tokens (via their LCS): 1 == identical, 0 == nothing shared.
func lineSim(a, b string) float64 {
	if a == b {
		return 1
	}
	ta, tb := words(a), words(b)
	if len(ta) == 0 && len(tb) == 0 {
		return 1
	}
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	dp := lcsTable(len(ta), len(tb), func(i, j int) bool { return ta[i] == tb[j] })
	return 2 * float64(dp[0][0]) / float64(len(ta)+len(tb))
}

// ── word-level diff of one substituted line pair ───────────────────────────

type tok struct {
	text       string
	start, end int // rune offsets into the line
}

// tokenize splits a line into alternating whitespace / non-whitespace runs,
// carrying each token's rune offsets so word ranges map back onto the line.
func tokenize(rs []rune) []tok {
	var toks []tok
	for i := 0; i < len(rs); {
		ws := unicode.IsSpace(rs[i])
		j := i + 1
		for j < len(rs) && unicode.IsSpace(rs[j]) == ws {
			j++
		}
		toks = append(toks, tok{string(rs[i:j]), i, j})
		i = j
	}
	return toks
}

func words(s string) []string {
	var out []string
	for _, t := range tokenize([]rune(s)) {
		if strings.TrimSpace(t.text) != "" {
			out = append(out, t.text)
		}
	}
	return out
}

// wordDiff LCS-matches the two lines' tokens and returns the rune ranges of the
// unmatched (changed) tokens on each side, coalesced into contiguous spans.
func wordDiff(ar, br []rune) (aRanges, bRanges [][2]int) {
	if len(ar) > 2000 || len(br) > 2000 { // pathological line — mark the whole thing
		if len(ar) > 0 {
			aRanges = [][2]int{{0, len(ar)}}
		}
		if len(br) > 0 {
			bRanges = [][2]int{{0, len(br)}}
		}
		return
	}
	ta, tb := tokenize(ar), tokenize(br)
	dp := lcsTable(len(ta), len(tb), func(i, j int) bool { return ta[i].text == tb[j].text })
	i, j := 0, 0
	for i < len(ta) && j < len(tb) {
		switch {
		case ta[i].text == tb[j].text:
			i, j = i+1, j+1
		case dp[i+1][j] >= dp[i][j+1]:
			aRanges = append(aRanges, [2]int{ta[i].start, ta[i].end})
			i++
		default:
			bRanges = append(bRanges, [2]int{tb[j].start, tb[j].end})
			j++
		}
	}
	for ; i < len(ta); i++ {
		aRanges = append(aRanges, [2]int{ta[i].start, ta[i].end})
	}
	for ; j < len(tb); j++ {
		bRanges = append(bRanges, [2]int{tb[j].start, tb[j].end})
	}
	return coalesce(aRanges), coalesce(bRanges)
}

func coalesce(ranges [][2]int) [][2]int {
	if len(ranges) < 2 {
		return ranges
	}
	sort.Slice(ranges, func(a, b int) bool { return ranges[a][0] < ranges[b][0] })
	out := [][2]int{ranges[0]}
	for k := 1; k < len(ranges); k++ {
		last := &out[len(out)-1]
		if ranges[k][0] <= last[1] {
			if ranges[k][1] > last[1] {
				last[1] = ranges[k][1]
			}
		} else {
			out = append(out, ranges[k])
		}
	}
	return out
}

// ── shared helpers ─────────────────────────────────────────────────────────

// lcsTable fills the standard suffix LCS-length table where eq(i,j) reports
// whether element i of the first sequence equals element j of the second.
// dp[0][0] is the LCS length; dp is used both for the length and for the
// greedy backtrack the callers run.
func lcsTable(n, m int, eq func(i, j int) bool) [][]int {
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if eq(i, j) {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	return dp
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

func esc(s string) template.HTML {
	return template.HTML(template.HTMLEscapeString(s))
}

// marked wraps the given (coalesced, sorted) rune ranges of line in
// <span class="d">…</span>, escaping every segment.
func marked(line []rune, ranges [][2]int) template.HTML {
	if len(ranges) == 0 {
		return esc(string(line))
	}
	var b strings.Builder
	pos := 0
	for _, rg := range ranges {
		s, e := rg[0], rg[1]
		if s < pos {
			s = pos
		}
		if e > len(line) {
			e = len(line)
		}
		if s >= e {
			continue
		}
		if s > pos {
			b.WriteString(template.HTMLEscapeString(string(line[pos:s])))
		}
		b.WriteString(`<span class="d">`)
		b.WriteString(template.HTMLEscapeString(string(line[s:e])))
		b.WriteString(`</span>`)
		pos = e
	}
	if pos < len(line) {
		b.WriteString(template.HTMLEscapeString(string(line[pos:])))
	}
	return template.HTML(b.String())
}
