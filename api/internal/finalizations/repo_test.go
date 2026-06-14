package finalizations_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"

	"github.com/greenmushrooms/job_searcher_web/api/internal/finalizations"
)

// openTestConn loads the project .env (walking up from this test file) and
// returns a single pgx.Conn. Single conn (not a pool) so the BEGIN/ROLLBACK in
// the test is the only tx on the wire; the repo runs its own nested tx inside.
func openTestConn(t *testing.T) *pgx.Conn {
	t.Helper()
	dir, _ := os.Getwd()
	for i := 0; i < 6 && dir != "/"; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
			_ = godotenv.Load(filepath.Join(dir, ".env"))
			break
		}
		dir = filepath.Dir(dir)
	}
	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		os.Getenv("DB_USER"),
		url.QueryEscape(os.Getenv("DB_PASSWORD")),
		os.Getenv("DB_HOST"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_NAME"),
	)
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Skipf("no DB available (%v); skipping integration test", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// TestSCD2_VersioningAndRestore exercises the full SCD Type 2 lifecycle inside a
// tx that is rolled back, so the DB is left unchanged.
func TestSCD2_VersioningAndRestore(t *testing.T) {
	ctx := context.Background()
	conn := openTestConn(t)

	// A real job_id keeps application_events FK/checks happy.
	var jobID string
	if err := conn.QueryRow(ctx,
		`SELECT id FROM public.jobspy_jobs ORDER BY date_posted DESC LIMIT 1`,
	).Scan(&jobID); err != nil {
		t.Skipf("no jobs in public.jobspy_jobs to test against: %v", err)
	}
	// Unique profile so version numbering starts clean regardless of prior data.
	profile := fmt.Sprintf("TestSCD2-%d", time.Now().UnixNano())

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	repo := finalizations.New(tx)

	// First save → version 1, current.
	f1, err := repo.Save(ctx, finalizations.SaveInput{JobID: jobID, SysProfile: profile, Markdown: "# v1"})
	if err != nil {
		t.Fatalf("save v1: %v", err)
	}
	if f1.Version != 1 || !f1.IsCurrent {
		t.Errorf("save v1: got version=%d current=%v, want 1/true", f1.Version, f1.IsCurrent)
	}

	// Second save → version 2 current; v1 retained (expired).
	f2, err := repo.Save(ctx, finalizations.SaveInput{JobID: jobID, SysProfile: profile, Markdown: "# v2"})
	if err != nil {
		t.Fatalf("save v2: %v", err)
	}
	if f2.Version != 2 || !f2.IsCurrent {
		t.Errorf("save v2: got version=%d current=%v, want 2/true", f2.Version, f2.IsCurrent)
	}

	// Get returns the current version (v2).
	cur, err := repo.Get(ctx, jobID, profile)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cur == nil || cur.Version != 2 || cur.Markdown != "# v2" {
		t.Fatalf("get current: got %+v, want version 2 markdown '# v2'", cur)
	}

	// History returns both, newest first; v1 is expired with valid_to set.
	hist, err := repo.History(ctx, jobID, profile)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("history len: got %d, want 2", len(hist))
	}
	if hist[0].Version != 2 || hist[1].Version != 1 {
		t.Errorf("history order: got %d,%d, want 2,1", hist[0].Version, hist[1].Version)
	}
	if hist[1].IsCurrent {
		t.Errorf("v1 should be expired (is_current=false)")
	}
	if hist[1].ValidTo == "" {
		t.Errorf("expired v1 should have valid_to set")
	}

	// Restore v1 → new version 3, current, carrying v1's markdown.
	f3, err := repo.Restore(ctx, jobID, profile, 1)
	if err != nil {
		t.Fatalf("restore v1: %v", err)
	}
	if f3.Version != 3 || !f3.IsCurrent || f3.Markdown != "# v1" {
		t.Errorf("restore: got version=%d current=%v markdown=%q, want 3/true/'# v1'", f3.Version, f3.IsCurrent, f3.Markdown)
	}

	// Exactly one current version (partial unique index invariant).
	var currentCount int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM web.jobs_resume WHERE job_id=$1 AND sys_profile=$2 AND is_current`,
		jobID, profile,
	).Scan(&currentCount); err != nil {
		t.Fatalf("count current: %v", err)
	}
	if currentCount != 1 {
		t.Errorf("current count: got %d, want 1", currentCount)
	}

	// Restoring a non-existent version errors and leaves the current intact.
	if _, err := repo.Restore(ctx, jobID, profile, 99); err == nil {
		t.Errorf("restore of missing version should error")
	}
	if cur, _ := repo.Get(ctx, jobID, profile); cur == nil || cur.Version != 3 {
		t.Errorf("current changed after failed restore: %+v", cur)
	}
}
