-- Master résumé as a free-form markdown document, one per profile. This is the
-- "original" the diff-lab left pane edits and saves permanently — distinct from
-- the per-job tailored copy in web.jobs_resume. Idempotent for cmd/migrate.

CREATE TABLE IF NOT EXISTS web.resume_master (
    sys_profile text        PRIMARY KEY,
    markdown    text        NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT NOW()
);
