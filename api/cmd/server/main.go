package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"github.com/greenmushrooms/job_searcher_web/api/internal/applications"
	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
	"github.com/greenmushrooms/job_searcher_web/api/internal/finalizations"
	"github.com/greenmushrooms/job_searcher_web/api/internal/handlers"
	"github.com/greenmushrooms/job_searcher_web/api/internal/jobs"
	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
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
	jobsRepo := jobs.New(pool)
	finalsRepo := finalizations.New(pool)
	resumeRepo := resume.NewRepo(pool)

	// Validate ?profile against the pipeline's known profiles (TTL-cached).
	profiles.Init(jobsRepo)

	dsClient, dsErr := deepseek.NewFromEnv()
	if dsErr != nil {
		log.Printf("deepseek: %v (resume tailoring endpoints will return 503)", dsErr)
	}

	jh := &handlers.JobsHandler{Repo: jobsRepo}
	ah := &handlers.ApplicationsHandler{Repo: appsRepo}
	uh := &handlers.UIHandler{Repo: appsRepo, Renderer: renderer}
	juh := &handlers.JobUIHandler{Jobs: jobsRepo, Apps: appsRepo, Renderer: renderer}
	rh := &handlers.ResumeHandler{
		Jobs:          jobsRepo,
		Finalizations: finalsRepo,
		Resumes:       resumeRepo,
		DeepSeek:      dsClient,
		Pool:          pool,
		Renderer:      renderer,
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// No global Timeout — set per-group below. chi's Timeout middleware
	// applies the shortest deadline when nested, so a global default would
	// shadow any longer per-route override.

	r.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(30 * time.Second))
			r.Get("/about", jh.About)
			r.Get("/profiles", jh.Profiles)
			r.Get("/jobs", jh.List)
			r.Get("/jobs/{id}", jh.Get)
			r.Post("/jobs/{id}/status", ah.SetStatus)
			r.Post("/jobs/{id}/finalize-resume", rh.Finalize)
		})
		// LLM endpoint: DeepSeek-v4-pro for ~40 bullets typically takes
		// 20-60s. 120s gives headroom without hanging indefinitely.
		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(120 * time.Second))
			r.Post("/jobs/{id}/draft-resume", rh.Draft)
		})
	})

	r.Route("/ui", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(30 * time.Second))
			r.Post("/jobs/{id}/status-row", uh.StatusRow)
			r.Get("/jobs/{id}/draft", rh.DraftFragment)
			r.Post("/jobs/{id}/generate", rh.GenerateResume)
			r.Get("/jobs/{id}/resume.pdf", rh.ResumePDF)

			// Server-rendered job list + summary + apply/skip (OOB row update).
			r.Get("/jobs", juh.JobList)
			r.Get("/jobs/{id}/workspace", juh.JobWorkspace)
			r.Get("/jobs/{id}/summary", juh.JobSummary)
			r.Post("/jobs/{id}/row-status", juh.RowStatus)

			// Left-hand canonical resume editor (htmx fragments).
			r.Get("/resume", rh.ResumeEditor)
			r.Post("/resume/bullets/{roleID}", rh.AddBullet)
			r.Post("/resume/bullets/{roleID}/{bulletID}", rh.SaveBullet)
			r.Post("/resume/bullets/{roleID}/{bulletID}/retire", rh.RemoveBullet)
		})
		// HTML trigger for a fresh DeepSeek draft — same 120s budget as the
		// JSON endpoint above.
		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(120 * time.Second))
			r.Post("/jobs/{id}/draft", rh.DraftFragmentTrigger)
		})
	})

	// Serve web/ as static, but never the templates/ subdir — those are Go
	// template sources rendered server-side, not public assets.
	fs := http.FileServer(http.Dir(webDir))
	r.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/templates/") {
			http.NotFound(w, req)
			return
		}
		fs.ServeHTTP(w, req)
	}))

	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	log.Printf("listening on %s (web=%s)", addr, webDir)
	log.Fatal(http.ListenAndServe(addr, r))
}
