-- Web schema: owned by job_searcher_web. Everything user-generated lives here
-- (applications, future resume_edits, application_events). Pipeline tables in
-- public.* are read-only from this service.
--
-- The prototype `public.applications` table (added by job_searcher_2's
-- migration 003, since removed from that repo) is left in place until the old
-- Flask dashboard's POST endpoint is retired. This migration copies its rows
-- forward so the new API can take over reads/writes without losing state.

CREATE SCHEMA IF NOT EXISTS web;

CREATE TABLE IF NOT EXISTS web.applications (
    job_id      text        NOT NULL,
    sys_profile text        NOT NULL,
    status      text        NOT NULL CHECK (status IN ('applied', 'skipped', 'interview')),
    notes       text,
    updated_at  timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (job_id, sys_profile)
);

CREATE INDEX IF NOT EXISTS applications_profile_status_idx
    ON web.applications (sys_profile, status);

-- One-time copy of the prototype data. ON CONFLICT DO NOTHING makes this safe
-- to re-run; the DO block skips if public.applications has already been dropped.
DO $$
BEGIN
    IF to_regclass('public.applications') IS NOT NULL THEN
        INSERT INTO web.applications (job_id, sys_profile, status, notes, updated_at)
        SELECT job_id, sys_profile, status, notes, updated_at
        FROM public.applications
        ON CONFLICT (job_id, sys_profile) DO NOTHING;
    END IF;
END $$;
