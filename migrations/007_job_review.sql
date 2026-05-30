-- Renames web.applications -> web.job_review: per-job, per-profile review state
-- (applied / skipped / interview + when). Same key as the eval (job_id), scoped
-- by sys_profile. "Unread" in the UI = no row here yet.
--
-- web.jobs_resume is logically keyed to this (same (job_id, sys_profile)); a
-- hard FK is intentionally NOT added, because a tailored resume can be
-- generated before any apply/skip decision exists — and "no row = unread"
-- must stay true.
--
-- public.applications (the legacy Flask table) is untouched; this only renames
-- the web-schema table this service owns. Guarded for the no-psql re-run path.

DO $$
BEGIN
    IF to_regclass('web.applications') IS NOT NULL
       AND to_regclass('web.job_review') IS NULL THEN
        ALTER TABLE web.applications RENAME TO job_review;
        ALTER INDEX IF EXISTS web.applications_profile_status_idx
            RENAME TO job_review_profile_status_idx;
    END IF;
END $$;
