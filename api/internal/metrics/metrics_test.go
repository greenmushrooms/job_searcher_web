package metrics

import "testing"

func ptr(s string) *string { return &s }

func TestParseAmt(t *testing.T) {
	cases := []struct {
		in   *string
		want float64
		ok   bool
	}{
		{ptr("120000"), 120000, true},
		{ptr("120.5"), 120.5, true},
		{ptr("$120,000"), 120000, true},
		{ptr("120k"), 120000, true},
		{ptr("85K"), 85000, true},
		{ptr(""), 0, false},
		{ptr("competitive"), 0, false},
		{nil, 0, false},
		{ptr("0"), 0, false},
	}
	for _, c := range cases {
		got, ok := parseAmt(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseAmt(%v) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestAnnualize(t *testing.T) {
	cases := []struct {
		v    float64
		iv   *string
		want float64
	}{
		{50, ptr("hourly"), 104000},
		{2000, ptr("weekly"), 104000},
		{10000, ptr("monthly"), 120000},
		{120000, ptr("yearly"), 120000},
		{120000, nil, 120000},
		{120000, ptr("WEEKLY"), 120000 * 52},
	}
	for _, c := range cases {
		if got := annualize(c.v, c.iv); got != c.want {
			t.Errorf("annualize(%v, %v) = %v, want %v", c.v, c.iv, got, c.want)
		}
	}
}

func TestAnnualSalary_MidpointAndFallback(t *testing.T) {
	if a, ok := annualSalary(ptr("100000"), ptr("140000"), ptr("yearly")); !ok || a != 120000 {
		t.Errorf("midpoint = %v,%v want 120000,true", a, ok)
	}
	if a, ok := annualSalary(ptr("90000"), nil, ptr("yearly")); !ok || a != 90000 {
		t.Errorf("min-only = %v,%v want 90000,true", a, ok)
	}
	if a, ok := annualSalary(ptr("50"), ptr("60"), ptr("hourly")); !ok || a != 55*2080 {
		t.Errorf("hourly midpoint = %v,%v want %v,true", a, ok, 55*2080)
	}
	if _, ok := annualSalary(ptr(""), ptr("n/a"), nil); ok {
		t.Error("unparseable should be ok=false")
	}
}

func TestSalaryBuckets(t *testing.T) {
	annuals := []float64{40000, 60000, 90000, 110000, 175000, 250000, 500000}
	bars := salaryBuckets(annuals)
	want := map[string]float64{
		"<50k": 1, "50–75k": 1, "75–100k": 1, "100–125k": 1,
		"125–150k": 0, "150–200k": 1, "200k+": 2,
	}
	for _, b := range bars {
		if b.Value != want[b.Label] {
			t.Errorf("bucket %s = %v, want %v", b.Label, b.Value, want[b.Label])
		}
	}
	if len(bars) != len(salaryRanges) {
		t.Errorf("got %d buckets, want %d", len(bars), len(salaryRanges))
	}
}
