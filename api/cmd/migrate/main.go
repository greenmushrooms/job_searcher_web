// Command migrate applies a single SQL migration file against the configured
// database. The deploy box has no psql, so this is how migrations land.
//
//	go run ./cmd/migrate -file ../migrations/006_jobs_resume.sql
//
// pgx's simple-protocol Exec runs the whole multi-statement file in one call.
// Migrations are expected to be written idempotently (IF EXISTS / DO guards)
// so a re-run is harmless.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"

	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
)

func main() {
	file := flag.String("file", "", "path to the .sql migration to apply")
	flag.Parse()
	if *file == "" {
		log.Fatal("-file is required")
	}

	for _, p := range []string{".env", "../.env", "../../.env"} {
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			break
		}
	}

	sql, err := os.ReadFile(*file)
	if err != nil {
		log.Fatalf("read %s: %v", *file, err)
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		log.Fatalf("apply %s: %v", *file, err)
	}
	fmt.Printf("applied %s\n", *file)
}
