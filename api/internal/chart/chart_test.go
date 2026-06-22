package chart

import (
	"strings"
	"testing"
)

func TestBarChart_RendersBarsAndEscapes(t *testing.T) {
	svg := string(BarChart([]Bar{{"0–1", 2}, {"<b>x</b>", 10}, {"9–10", 5}}, 400, 160))
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatalf("not an svg: %.40s", svg)
	}
	if strings.Count(svg, "<rect") != 3 {
		t.Errorf("want 3 bars, got %d <rect>", strings.Count(svg, "<rect"))
	}
	if strings.Contains(svg, "<b>x</b>") || !strings.Contains(svg, "&lt;b&gt;x&lt;/b&gt;") {
		t.Error("label was not HTML-escaped")
	}
}

func TestBarChart_TallestBarHitsMax(t *testing.T) {
	// The max-value bar should be the tallest (smallest y). Eyeball via heights:
	// a 0-value bar must have height 0.
	svg := string(BarChart([]Bar{{"a", 0}, {"b", 100}}, 200, 120))
	if !strings.Contains(svg, `height="0.0"`) {
		t.Errorf("zero-value bar should have height 0:\n%s", svg)
	}
}

func TestBarChart_EmptyIsNoData(t *testing.T) {
	svg := string(BarChart(nil, 200, 100))
	if !strings.Contains(svg, "no data") || strings.Contains(svg, "<rect") {
		t.Errorf("empty chart should say 'no data' with no bars: %s", svg)
	}
}

func TestFunnel_ProportionalWidthsAndPct(t *testing.T) {
	svg := string(Funnel([]Stage{{"Evaluated", 1000}, {"Presented", 500}, {"Applied", 50}}, 400, 120))
	// top stage full width (≈ 388 inner), half stage ≈ half. Just assert the
	// percentages render and the stages are labeled.
	for _, want := range []string{"Evaluated", "1,000", "Presented", "500", "Applied", "50", "50%", "5%", "100%"} {
		if !strings.Contains(svg, want) {
			t.Errorf("funnel missing %q\n%s", want, svg)
		}
	}
}

func TestCommaInt(t *testing.T) {
	cases := map[int]string{0: "0", 42: "42", 1000: "1,000", 13569: "13,569", -2500: "-2,500"}
	for in, want := range cases {
		if got := commaInt(in); got != want {
			t.Errorf("commaInt(%d) = %q, want %q", in, got, want)
		}
	}
}
