package applications_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"

	"github.com/greenmushrooms/job_searcher_web/api/internal/applications"
)

// openTestConn loads the project .env (walking up from this test file) and
// returns a single pgx.Conn. Single conn (not a pool) so the BEGIN/ROLLBACK
// in the test is the only tx on the wire and isolation is trivial.
func openTestConn(t *testing.T) *pgx.Conn {
	t.Helper()
	// Walk up from this file's dir until we find a .env.
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

// TestUpsert_HappyPath posts a status change inside a tx, asserts the row is
// in web.applications and an event is in web.application_events, then rolls
// back so the DB is unchanged.
func TestUpsert_HappyPath(t *testing.T) {
	ctx := context.Background()
	conn := openTestConn(t)

	// Real job_id from the pipeline — needed so the existence check passes.
	var jobID string
	if err := conn.QueryRow(ctx,
		`SELECT id FROM public.jobspy_jobs ORDER BY date_posted DESC LIMIT 1`,
	).Scan(&jobID); err != nil {
		t.Skipf("no jobs in public.jobspy_jobs to test against: %v", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	repo := applications.New(tx)
	notes := "applied via cover letter v2"
	app, err := repo.Upsert(ctx, jobID, "TestProfile", "applied", &notes)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if app.Status != "applied" {
		t.Errorf("status: got %q want %q", app.Status, "applied")
	}
	if app.Notes == nil || *app.Notes != notes {
		t.Errorf("notes: got %v want %q", app.Notes, notes)
	}

	// Row landed in web.applications.
	var dbStatus string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM web.applications WHERE job_id = $1 AND sys_profile = $2`,
		jobID, "TestProfile",
	).Scan(&dbStatus); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if dbStatus != "applied" {
		t.Errorf("db status: got %q want %q", dbStatus, "applied")
	}

	// Event was appended.
	var eventCount int
	if err := tx.QueryRow(ctx, `
        SELECT count(*) FROM web.application_events
        WHERE job_id = $1 AND sys_profile = $2 AND event_type = 'status_changed'
    `, jobID, "TestProfile").Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("event count: got %d want 1", eventCount)
	}

	// Second upsert on same key should UPDATE (still one row) but write a
	// second event (status changes are tracked individually).
	if _, err := repo.Upsert(ctx, jobID, "TestProfile", "interview", nil); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	var rowCount int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM web.applications WHERE job_id = $1 AND sys_profile = $2`,
		jobID, "TestProfile",
	).Scan(&rowCount); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("row count after second upsert: got %d want 1", rowCount)
	}
	if err := tx.QueryRow(ctx, `
        SELECT count(*) FROM web.application_events
        WHERE job_id = $1 AND sys_profile = $2 AND event_type = 'status_changed'
    `, jobID, "TestProfile").Scan(&eventCount); err != nil {
		t.Fatalf("count events 2: %v", err)
	}
	if eventCount != 2 {
		t.Errorf("event count after second upsert: got %d want 2", eventCount)
	}
}

func TestUpsert_InvalidStatus(t *testing.T) {
	ctx := context.Background()
	conn := openTestConn(t)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	repo := applications.New(tx)
	_, err = repo.Upsert(ctx, "anything", "TestProfile", "bogus", nil)
	if !errors.Is(err, applications.ErrInvalidStatus) {
		t.Errorf("expected ErrInvalidStatus, got %v", err)
	}
}

func TestUpsert_JobNotFound(t *testing.T) {
	ctx := context.Background()
	conn := openTestConn(t)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	repo := applications.New(tx)
	_, err = repo.Upsert(ctx, "definitely-not-a-real-job-id", "TestProfile", "applied", nil)
	if !errors.Is(err, applications.ErrJobNotFound) {
		t.Errorf("expected ErrJobNotFound, got %v", err)
	}
}
