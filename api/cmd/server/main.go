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
	"github.com/greenmushrooms/job_searcher_web/api/internal/coverletters"
	"github.com/greenmushrooms/job_searcher_web/api/internal/db"
	"github.com/greenmushrooms/job_searcher_web/api/internal/deepseek"
	"github.com/greenmushrooms/job_searcher_web/api/internal/finalizations"
	"github.com/greenmushrooms/job_searcher_web/api/internal/handlers"
	"github.com/greenmushrooms/job_searcher_web/api/internal/jobs"
	"github.com/greenmushrooms/job_searcher_web/api/internal/profiles"
	"github.com/greenmushrooms/job_searcher_web/api/internal/render"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resume"
	"github.com/greenmushrooms/job_searcher_web/api/internal/resumemaster"
	"github.com/greenmushrooms/job_searcher_web/api/internal/templates"
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
	coverRepo := coverletters.New(pool)
	masterRepo := resumemaster.New(pool)
	resumeRepo := resume.NewRepo(pool)
	templatesRepo := templates.New(pool)

	// Validate ?profile against the pipeline's known profiles (TTL-cached).
	profiles.Init(jobsRepo)

	// Per-user profile isolation. PROFILE_ACCESS maps the proxy's Remote-User to
	// the one profile that account may see (e.g. "slava:Slava,kezia:Kezia").
	// Unset => no isolation (local dev). See handlers.RestrictProfile for the
	// trust model.
	access, err := handlers.ParseProfileAccess(os.Getenv("PROFILE_ACCESS"))
	if err != nil {
		log.Fatalf("PROFILE_ACCESS: %v", err)
	}
	if len(access) > 0 {
		log.Printf("profile isolation enabled for %d account(s)", len(access))
	}

	dsClient, dsErr := deepseek.NewFromEnv()
	if dsErr != nil {
		log.Printf("deepseek: %v (resume tailoring endpoints will return 503)", dsErr)
	}

	jh := &handlers.JobsHandler{Repo: jobsRepo}
	ah := &handlers.ApplicationsHandler{Repo: appsRepo}
	uh := &handlers.UIHandler{Repo: appsRepo, Renderer: renderer}
	juh := &handlers.JobUIHandler{Jobs: jobsRepo, Apps: appsRepo, Templates: templatesRepo, Renderer: renderer}
	rh := &handlers.ResumeHandler{
		Jobs:          jobsRepo,
		Finalizations: finalsRepo,
		CoverLetters:  coverRepo,
		Master:        masterRepo,
		Resumes:       resumeRepo,
		Templates:     templatesRepo,
		DeepSeek:      dsClient,
		Pool:          pool,
		Renderer:      renderer,
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(handlers.RestrictProfile(access)) // per-user profile isolation (no-op when unset)
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
		// 20-110s+. 180s gives headroom without hanging indefinitely.
		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(180 * time.Second))
			r.Post("/jobs/{id}/draft-resume", rh.Draft)
		})
	})

	r.Route("/ui", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(30 * time.Second))
			r.Post("/jobs/{id}/status-row", uh.StatusRow)
			r.Get("/jobs/{id}/draft", rh.DraftFragment)
			r.Post("/jobs/{id}/apply-ai", rh.ApplyAI)
			r.Post("/jobs/{id}/generate", rh.GenerateResume)
			r.Post("/jobs/{id}/save-template", rh.SaveTemplate)
			r.Post("/jobs/{id}/replace-template", rh.ReplaceTemplate)
			r.Get("/jobs/{id}/resume.pdf", rh.ResumePDF)
			r.Post("/jobs/{id}/resume.pdf", rh.GeneratePDF)
			r.Get("/jobs/{id}/resume/versions", rh.ResumeVersions)                 // SCD2 version history
			r.Post("/jobs/{id}/resume/versions/{version}/restore", rh.RestoreResumeVersion)
			r.Get("/jobs/{id}/cover-letter", rh.CoverLetterFragment)
			r.Post("/jobs/{id}/cover-letter", rh.SaveCoverLetter)
			r.Post("/jobs/{id}/cover-letter.pdf", rh.CoverLetterPDF)
			r.Post("/resume/master", rh.SaveMaster) // diff lab: permanent master save
			r.Post("/difflab/diff", rh.DiffLabCompute) // diff lab v4: zero-JS recompute

			// Resume template manager (rename / delete / set default).
			r.Get("/resume/templates", rh.TemplatesManager)
			r.Post("/resume/templates/{templateID}/rename", rh.RenameTemplate)
			r.Post("/resume/templates/{templateID}/delete", rh.DeleteTemplate)
			r.Post("/resume/templates/{templateID}/default", rh.SetDefaultTemplate)

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
		// HTML triggers that call DeepSeek — same 180s budget as the JSON
		// endpoint above.
		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(180 * time.Second))
			r.Post("/jobs/{id}/draft", rh.DraftFragmentTrigger)
			r.Post("/jobs/{id}/cover-letter/draft", rh.DraftCoverLetter)
		})
	})

	// Land on the current résumé workspace (jobs.html) rather than the older
	// index.html prototype.
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/jobs.html", http.StatusFound)
	})

	// Diff-lab comparison pages — highlighting variants of the two-pane
	// master-vs-job résumé editor, to pick one. v4 is the zero-JS, server-
	// rendered diff (no CodeMirror): it recomputes via POST /ui/difflab/diff.
	r.Get("/v1", func(w http.ResponseWriter, req *http.Request) { rh.DiffLab(w, req, "v1") })
	r.Get("/v2", func(w http.ResponseWriter, req *http.Request) { rh.DiffLab(w, req, "v2") })
	r.Get("/v3", func(w http.ResponseWriter, req *http.Request) { rh.DiffLab(w, req, "v3") })
	r.Get("/v4", func(w http.ResponseWriter, req *http.Request) { rh.DiffLab(w, req, "v4") })

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
		addr = ":7770"
	}
	log.Printf("listening on %s (web=%s)", addr, webDir)
	log.Fatal(http.ListenAndServe(addr, r))
}
