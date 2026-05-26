-- Audit trail + first-event-of-its-kind timestamp for web.applications.
--
-- created_at: set to updated_at for existing rows (best approximation we have
-- for "first action"); going forward it's set once on INSERT and never changed.
--
-- web.application_events: append-only log of everything the user (or LLM)
-- does to a job. event_type is text+CHECK rather than an enum so the vocab
-- can evolve via a one-line ALTER instead of a multi-step type migration.

ALTER TABLE web.applications
    ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT NOW();

UPDATE web.applications
SET    created_at = updated_at
WHERE  created_at > updated_at;  -- only the rows DEFAULT NOW() just stamped

CREATE TABLE IF NOT EXISTS web.application_events (
    id          bigserial   PRIMARY KEY,
    sys_profile text        NOT NULL,
    job_id      text        NOT NULL,
    event_type  text        NOT NULL CHECK (event_type IN (
                    'status_changed',
                    'viewed',
                    'resume_drafted',
                    'resume_finalized'
                )),
    payload     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS application_events_profile_job_time_idx
    ON web.application_events (sys_profile, job_id, created_at DESC);

CREATE INDEX IF NOT EXISTS application_events_type_time_idx
    ON web.application_events (event_type, created_at DESC);
