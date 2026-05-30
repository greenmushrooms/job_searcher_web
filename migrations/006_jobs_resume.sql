-- Renames web.resume_finalizations -> web.jobs_resume: the per-job tailored
-- resume record. "Generate" (was "Finalize") writes here.
--
-- Adds `removals` — a snapshot of the LLM's removal diff at generate time — so
-- the row self-describes the tailoring decision without joining the event log.
-- finalized_at -> generated_at to match the action.
--
-- Keyed by (job_id, sys_profile), the same key as web.applications (the future
-- job_review). A FK to job_review is deferred until that table is settled in
-- the next slice; generating a resume must not require an applications row.
--
-- Guarded so a re-run on the no-psql migrate path is harmless.

DO $$
BEGIN
    IF to_regclass('web.resume_finalizations') IS NOT NULL
       AND to_regclass('web.jobs_resume') IS NULL THEN
        ALTER TABLE web.resume_finalizations RENAME TO jobs_resume;
        ALTER INDEX IF EXISTS web.resume_finalizations_profile_time_idx
            RENAME TO jobs_resume_profile_time_idx;
    END IF;

    IF to_regclass('web.jobs_resume') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1 FROM information_schema.columns
           WHERE table_schema = 'web' AND table_name = 'jobs_resume'
             AND column_name = 'generated_at'
       ) THEN
        ALTER TABLE web.jobs_resume RENAME COLUMN finalized_at TO generated_at;
    END IF;
END $$;

ALTER TABLE web.jobs_resume
    ADD COLUMN IF NOT EXISTS removals jsonb NOT NULL DEFAULT '[]'::jsonb;

-- "Generate" writes a resume_generated event; the event_type CHECK predates
-- the rename. Replace it to allow the new type (old types kept for existing
-- rows, including the legacy resume_finalized events).
ALTER TABLE web.application_events
    DROP CONSTRAINT IF EXISTS application_events_event_type_check;
ALTER TABLE web.application_events
    ADD CONSTRAINT application_events_event_type_check
    CHECK (event_type IN (
        'status_changed', 'viewed', 'resume_drafted',
        'resume_finalized', 'resume_generated'
    ));
