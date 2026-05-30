package handlers

import (
	"reflect"
	"testing"
)

func TestGroupByRole(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want map[string][]string
	}{
		{
			name: "groups and preserves order",
			in:   []string{"r1.b1", "r1.b2", "r2.b9"},
			want: map[string][]string{"r1": {"b1", "b2"}, "r2": {"b9"}},
		},
		{
			name: "skips ids without a dot",
			in:   []string{"r1.b1", "noseparator"},
			want: map[string][]string{"r1": {"b1"}},
		},
		{
			name: "splits on first dot only",
			in:   []string{"r1.b.x"},
			want: map[string][]string{"r1": {"b.x"}},
		},
		{
			name: "empty input",
			in:   []string{},
			want: map[string][]string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := groupByRole(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
