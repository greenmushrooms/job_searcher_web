// Package chart renders small, dependency-free inline SVG charts as escaped
// HTML strings — the zero-JS counterpart to a client charting library, in the
// same spirit as package resumediff. Handlers compute the data; this turns it
// into an <svg> the template drops in verbatim.
package chart

import (
	"fmt"
	"html/template"
	"strings"
)

// Bar is one category in a bar chart: a label and a numeric value.
type Bar struct {
	Label string
	Value float64
}

// Stage is one step of a funnel: a label and an absolute count.
type Stage struct {
	Label string
	Count int
}

const (
	barFill    = "#4c78a8"
	funnelFill = "#4c78a8"
	axis       = "#cbd5e1"
	textColor  = "#334155"
	muted      = "#64748b"
)

// BarChart renders a labeled vertical bar chart. width/height are the SVG
// viewBox in px; the caller picks a size that fits its column. An empty bars
// slice renders a small "no data" note so the page never shows a broken chart.
func BarChart(bars []Bar, width, height int) template.HTML {
	if len(bars) == 0 {
		return empty(width, height)
	}
	const padL, padR, padT, padB = 6, 6, 14, 26
	innerW := float64(width - padL - padR)
	innerH := float64(height - padT - padB)
	baseY := float64(height - padB)

	maxV := 0.0
	for _, b := range bars {
		if b.Value > maxV {
			maxV = b.Value
		}
	}
	if maxV <= 0 {
		maxV = 1
	}
	slot := innerW / float64(len(bars))
	bw := slot * 0.68

	var b strings.Builder
	svgOpen(&b, width, height)
	fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="%s"/>`,
		padL, baseY, width-padR, baseY, axis)
	for i, bar := range bars {
		x := float64(padL) + float64(i)*slot + (slot-bw)/2
		h := bar.Value / maxV * innerH
		y := baseY - h
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s" rx="1.5"><title>%s: %s</title></rect>`,
			x, y, bw, h, barFill, esc(bar.Label), num(bar.Value))
		if bar.Value > 0 {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle" font-size="9" fill="%s">%s</text>`,
				x+bw/2, y-2, muted, num(bar.Value))
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle" font-size="9" fill="%s">%s</text>`,
			x+bw/2, baseY+11, textColor, esc(bar.Label))
	}
	b.WriteString("</svg>")
	return template.HTML(b.String())
}

// Funnel renders stages as stacked horizontal bars whose width is proportional
// to the first (largest) stage, each annotated with its count and the % of the
// top stage retained — a flow from evaluated down to applied.
func Funnel(stages []Stage, width, height int) template.HTML {
	if len(stages) == 0 {
		return empty(width, height)
	}
	top := stages[0].Count
	if top <= 0 {
		top = 1
	}
	const padL, padR, padT = 6, 6, 6
	rowH := float64(height-padT) / float64(len(stages))
	barH := rowH * 0.62
	fullW := float64(width - padL - padR)

	var b strings.Builder
	svgOpen(&b, width, height)
	for i, s := range stages {
		y := float64(padT) + float64(i)*rowH
		w := fullW * float64(s.Count) / float64(top)
		if w < 2 && s.Count > 0 {
			w = 2
		}
		// track
		fmt.Fprintf(&b, `<rect x="%d" y="%.1f" width="%.1f" height="%.1f" fill="#eef2f7" rx="2"/>`,
			padL, y, fullW, barH)
		fmt.Fprintf(&b, `<rect x="%d" y="%.1f" width="%.1f" height="%.1f" fill="%s" rx="2"/>`,
			padL, y, w, barH, funnelFill)
		pct := 100 * float64(s.Count) / float64(top)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" font-size="10" font-weight="600" fill="#fff">%s %s</text>`,
			padL+6, y+barH*0.68, esc(s.Label), commaInt(s.Count))
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" text-anchor="end" font-size="9" fill="%s">%.0f%%</text>`,
			width-padR-3, y+barH*0.68, muted, pct)
	}
	b.WriteString("</svg>")
	return template.HTML(b.String())
}

func svgOpen(b *strings.Builder, w, h int) {
	fmt.Fprintf(b, `<svg viewBox="0 0 %d %d" width="100%%" height="%d" role="img" font-family="system-ui,sans-serif">`, w, h, h)
}

func empty(w, h int) template.HTML {
	var b strings.Builder
	svgOpen(&b, w, h)
	fmt.Fprintf(&b, `<text x="%d" y="%d" text-anchor="middle" font-size="11" fill="%s">no data</text>`, w/2, h/2, muted)
	b.WriteString("</svg>")
	return template.HTML(b.String())
}

func esc(s string) string { return template.HTMLEscapeString(s) }

// num formats a value as an integer when whole, else one decimal.
func num(v float64) string {
	if v == float64(int64(v)) {
		return commaInt(int(v))
	}
	return fmt.Sprintf("%.1f", v)
}

// commaInt formats an int with thousands separators (e.g. 13569 → "13,569").
func commaInt(n int) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
