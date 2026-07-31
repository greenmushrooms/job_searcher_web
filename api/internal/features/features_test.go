package features

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// stubRow returns a canned Scan result, standing in for pgx.Row.
type stubRow struct {
	val bool
	err error
}

func (s stubRow) Scan(dest ...any) error {
	if s.err != nil {
		return s.err
	}
	if p, ok := dest[0].(*bool); ok {
		*p = s.val
	}
	return nil
}

type stubQuerier struct{ row stubRow }

func (q stubQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return q.row
}
func (q stubQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, errors.New("not used")
}
func (q stubQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("not used")
}

// A gate must fail closed. Every failure mode here — no row, dead pool, blank
// input — has to report "off", because reporting "on" by accident exposes a
// feature to a profile that never asked for it.
func TestEnabledFailsClosed(t *testing.T) {
	ctx := context.Background()

	t.Run("explicitly enabled", func(t *testing.T) {
		if !Enabled(ctx, stubQuerier{stubRow{val: true}}, "Slava", Practice) {
			t.Error("want true when the row says enabled")
		}
	})

	t.Run("explicitly disabled", func(t *testing.T) {
		if Enabled(ctx, stubQuerier{stubRow{val: false}}, "Slava", Practice) {
			t.Error("want false when the row says disabled")
		}
	})

	t.Run("no row means off, not on", func(t *testing.T) {
		if Enabled(ctx, stubQuerier{stubRow{err: pgx.ErrNoRows}}, "Kezia", Practice) {
			t.Error("want false when the profile has no row — flags are opt-in")
		}
	})

	t.Run("a broken query means off", func(t *testing.T) {
		if Enabled(ctx, stubQuerier{stubRow{err: errors.New("relation does not exist")}}, "Slava", Practice) {
			t.Error("want false when the lookup errors — e.g. an unmigrated box")
		}
	})

	t.Run("blank inputs are off without touching the db", func(t *testing.T) {
		panicky := stubQuerier{stubRow{err: errors.New("should not be called")}}
		if Enabled(ctx, panicky, "", Practice) {
			t.Error("want false for an empty profile")
		}
		if Enabled(ctx, panicky, "Slava", "") {
			t.Error("want false for an empty feature")
		}
	})
}
