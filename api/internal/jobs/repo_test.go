package jobs

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeQuerier records the SQL + args of the last Query call and returns an
// empty result set. It lets us assert the dynamic WHERE / $N index building in
// List without a database.
type fakeQuerier struct {
	lastSQL  string
	lastArgs []any
}

func (f *fakeQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.lastSQL, f.lastArgs = sql, args
	return &emptyRows{}, nil
}
func (f *fakeQuerier) QueryRow(context.Context, string, ...any) pgx.Row { return nil }
func (f *fakeQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

// emptyRows is a no-row pgx.Rows: Next() is immediately false so queryJobs
// returns an empty slice without ever scanning.
type emptyRows struct{}

func (*emptyRows) Close()                                       {}
func (*emptyRows) Err() error                                   { return nil }
func (*emptyRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (*emptyRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (*emptyRows) Next() bool                                   { return false }
func (*emptyRows) Scan(...any) error                            { return nil }
func (*emptyRows) Values() ([]any, error)                       { return nil, nil }
func (*emptyRows) RawValues() [][]byte                          { return nil }
func (*emptyRows) Conn() *pgx.Conn                              { return nil }

func TestList_ArgIndexing(t *testing.T) {
	tests := []struct {
		name       string
		params     ListParams
		wantArgs   []any
		wantSQLHas []string
	}{
		{
			name:     "no date filters",
			params:   ListParams{Profile: "Slava", MinScore: 6.9, Limit: 50, Offset: 0},
			wantArgs: []any{"Slava", 6.9, 50, 0},
			// limit/offset are the 3rd/4th args.
			wantSQLHas: []string{"LIMIT $3 OFFSET $4", "e.sys_profile = $1", "e.avg_score >= $2"},
		},
		{
			name:     "from only, posted date field",
			params:   ListParams{Profile: "Cait", MinScore: 7, Limit: 10, Offset: 5, From: "2026-01-01", DateField: "posted"},
			wantArgs: []any{"Cait", 7.0, "2026-01-01", 10, 5},
			wantSQLHas: []string{
				"j.date_posted >= $3",
				"LIMIT $4 OFFSET $5",
			},
		},
		{
			name:     "from and to, eval date field",
			params:   ListParams{Profile: "Ray", MinScore: 8, Limit: 25, Offset: 0, From: "2026-01-01", To: "2026-02-01", DateField: "eval"},
			wantArgs: []any{"Ray", 8.0, "2026-01-01", "2026-02-01", 25, 0},
			wantSQLHas: []string{
				"e.created_at::date >= $3",
				"e.created_at::date <= $4",
				"LIMIT $5 OFFSET $6",
			},
		},
		{
			name:     "to only",
			params:   ListParams{Profile: "Kezia", MinScore: 6, Limit: 100, Offset: 200, To: "2026-03-15", DateField: "eval"},
			wantArgs: []any{"Kezia", 6.0, "2026-03-15", 100, 200},
			wantSQLHas: []string{
				"e.created_at::date <= $3",
				"LIMIT $4 OFFSET $5",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fq := &fakeQuerier{}
			repo := New(fq)
			if _, err := repo.List(context.Background(), tc.params); err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(fq.lastArgs) != len(tc.wantArgs) {
				t.Fatalf("arg count: got %d %v, want %d %v",
					len(fq.lastArgs), fq.lastArgs, len(tc.wantArgs), tc.wantArgs)
			}
			for i := range tc.wantArgs {
				if fq.lastArgs[i] != tc.wantArgs[i] {
					t.Errorf("arg[%d]: got %v (%T), want %v (%T)",
						i, fq.lastArgs[i], fq.lastArgs[i], tc.wantArgs[i], tc.wantArgs[i])
				}
			}
			for _, frag := range tc.wantSQLHas {
				if !strings.Contains(fq.lastSQL, frag) {
					t.Errorf("SQL missing %q\n--- SQL ---\n%s", frag, fq.lastSQL)
				}
			}
		})
	}
}

func TestListLite_SlimColumnsSamePaging(t *testing.T) {
	fq := &fakeQuerier{}
	repo := New(fq)
	p := ListParams{Profile: "Slava", MinScore: 6.9, Limit: 50, Offset: 0, From: "2026-01-01", DateField: "eval"}
	if _, err := repo.ListLite(context.Background(), p); err != nil {
		t.Fatalf("ListLite: %v", err)
	}
	if strings.Contains(fq.lastSQL, "j.description") || strings.Contains(fq.lastSQL, "e.reasoning") {
		t.Errorf("ListLite should not select description/reasoning:\n%s", fq.lastSQL)
	}
	// Same filtering + paging as List: from is $3, limit/offset $4/$5.
	for _, frag := range []string{"e.created_at::date >= $3", "LIMIT $4 OFFSET $5"} {
		if !strings.Contains(fq.lastSQL, frag) {
			t.Errorf("ListLite SQL missing %q\n%s", frag, fq.lastSQL)
		}
	}
	wantArgs := []any{"Slava", 6.9, "2026-01-01", 50, 0}
	if len(fq.lastArgs) != len(wantArgs) {
		t.Fatalf("args: got %v, want %v", fq.lastArgs, wantArgs)
	}
	for i := range wantArgs {
		if fq.lastArgs[i] != wantArgs[i] {
			t.Errorf("arg[%d]: got %v, want %v", i, fq.lastArgs[i], wantArgs[i])
		}
	}
}

func TestParseAmount(t *testing.T) {
	i64 := func(v int64) *int64 { return &v }
	str := func(s string) *string { return &s }
	tests := []struct {
		name string
		in   *string
		want *int64
	}{
		{"nil", nil, nil},
		{"empty", str(""), nil},
		{"whitespace", str("  "), nil},
		{"plain int", str("120000"), i64(120000)},
		{"padded int", str(" 95000 "), i64(95000)},
		{"float truncates", str("120000.75"), i64(120000)},
		{"non-numeric", str("120k"), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseAmount(tc.in)
			switch {
			case got == nil && tc.want == nil:
				// ok
			case got == nil || tc.want == nil:
				t.Fatalf("got %v, want %v", got, tc.want)
			case *got != *tc.want:
				t.Errorf("got %d, want %d", *got, *tc.want)
			}
		})
	}
}
