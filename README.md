# job_searcher_web

User-facing layer on top of the `job_searcher_2` data pipeline.

The pipeline (separate project) scrapes and scores jobs, writing to `public.jobspy_jobs` and `public.evaluated_jobs`. This project owns everything user-facing on top of that data: an HTTP API plus an htmx UI for triaging good jobs, recording application status, and (later) generating tailored resumes via DeepSeek.

```
[data_eng pipeline] --writes--> [Postgres: public.*]
                                       ^
                                       | reads
                                       |
[htmx UI] <--->  [Go API]  <-----------+
                    |
                    +--writes--> [Postgres: web.*]  (applications, resume_edits, ...)
                    |
                    +--calls---> [DeepSeek API]     (resume tailoring, later)
```

Boundaries:
- The pipeline's `public.*` tables are read-only from this project.
- This project's writes go to the `web.*` schema only.
- The pipeline does not import or call this project.

## Layout

```
job_searcher_web/
├── migrations/         # SQL migrations for the web schema
├── api/                # Go HTTP API (chi + pgxpool)
│   ├── cmd/server/     # main.go entry point
│   └── internal/
│       ├── db/         # pgxpool init
│       ├── jobs/       # SQL + types
│       └── handlers/   # HTTP handlers
└── web/                # htmx static assets (served by api)
```

## Running locally

```bash
# 1. Apply migrations (uses the same Postgres as the pipeline)
PGPASSWORD=$(grep DB_PASSWORD ../job_searcher_2/.env | cut -d= -f2) \
  psql -h localhost -U user_job_searcher -d job_searcher \
  -f migrations/001_web_schema.sql

# 2. Start the API (also serves web/ as static)
cd api
cp ../.env.example .env   # then fill in DB_* values
go run ./cmd/server
# API on :7770, htmx UI on http://localhost:7770/
```

## Migration history

- `001_web_schema.sql` — creates `web` schema and `web.applications`. Copies the 10 prototype rows from `public.applications` (the table created by `job_searcher_2/migrations/003_applications.sql`, which has been removed from that repo's history of new migrations and is now owned here).
- `public.applications` is intentionally left in place until the Flask dashboard's POST `/api/v1/jobs/<id>/status` is retired in favour of this service.
- `005_user_profile.sql` — moves the canonical resume out of `resume_htmx/resume_data.json` into `web.user_profile` + `web.resume_roles` / `web.resume_bullets` / `web.resume_skills` / `web.resume_education`, keyed by `sys_profile`. `resume.Load()` now reads from here.

Apply + import (the box has no `psql`, so `cmd/seed-resume` applies the DDL and loads the JSON in one step):

```bash
cd api && go run ./cmd/seed-resume          # profile=Slava, file from RESUME_JSON_PATH
# re-run any time to re-sync the DB with edits still made in resume_htmx
```
- `006_jobs_resume.sql` — renames `web.resume_finalizations` → `web.jobs_resume` (the per-job tailored resume), adds a `removals` snapshot + `generated_at`, and widens the `application_events` event-type CHECK to allow `resume_generated`.
- `007_job_review.sql` — renames `web.applications` → `web.job_review` (per-job review state: applied/skipped/interview). "Unread" in the UI = no row here yet.
- `011_cover_letter.sql` — adds `web.jobs_cover_letter` (per-job AI-drafted, hand-edited cover letter) and widens the event-type CHECK to allow `cover_letter_drafted` / `cover_letter_saved`.

Apply any later migration with the generic runner (no `psql` needed):

```bash
cd api && go run ./cmd/migrate -file ../migrations/007_job_review.sql
```
