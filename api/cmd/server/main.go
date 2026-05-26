package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"github.com/greenmushrooms/job_searcher_web/api/internal/applications"
	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
	"github.com/greenmushrooms/job_searcher_web/api/internal/handlers"
	"github.com/greenmushrooms/job_searcher_web/api/internal/jobs"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
)

func main() {
	// Load .env from the repo root, the api/ dir, or cwd — whichever exists first.
	for _, p := range []string{".env", "../.env", "../../.env"} {
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			break
		}
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	// Static htmx UI under ./web; templates under ./web/templates.
	webDir := os.Getenv("WEB_DIR")
	if webDir == "" {
		webDir = "../web"
	}
	renderer, err := render.New(webDir + "/templates")
	if err != nil {
		log.Fatalf("render: %v", err)
	}

	appsRepo := applications.New(pool)
	jh := &handlers.JobsHandler{Repo: jobs.New(pool)}
	ah := &handlers.ApplicationsHandler{Repo: appsRepo}
	uh := &handlers.UIHandler{Repo: appsRepo, Renderer: renderer}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/about", jh.About)
		r.Get("/profiles", jh.Profiles)
		r.Get("/jobs", jh.List)
		r.Get("/jobs/{id}", jh.Get)
		r.Post("/jobs/{id}/status", ah.SetStatus)
	})

	r.Route("/ui", func(r chi.Router) {
		r.Post("/jobs/{id}/status-row", uh.StatusRow)
	})

	r.Handle("/*", http.FileServer(http.Dir(webDir)))

	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	log.Printf("listening on %s (web=%s)", addr, webDir)
	log.Fatal(http.ListenAndServe(addr, r))
}
