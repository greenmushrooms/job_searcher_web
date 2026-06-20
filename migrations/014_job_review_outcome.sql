-- Two-axis review state on web.job_review: separate "where it is in the funnel"
-- (status) from "how it ended" (final_status).
--
--   status       — furthest active stage: applied → screen → interview (+ skipped).
--   final_status — terminal outcome: 'rejected' or 'offer'. NULL = still open;
--                  an application that never gets an outcome is, in effect, ghosted.
--   final_at     — when the outcome was recorded, for "recently closed" sorting.
--
-- web.application_events now logs typed events (applied/screen/interview/
-- rejected/offer/…) instead of a generic 'status_changed', so the funnel is
-- reconstructable straight from the log. The legacy 'status_changed' value stays
-- in the CHECK so pre-migration rows remain valid.
--
-- Inline CHECKs get deterministic names from the column they guard: the status
-- check was born as applications_status_check (the table's name in 001, kept
-- through the 007 rename) and the event check as application_events_event_type_check
-- (002). We drop those, then add the same constraints under stable names.
-- Idempotent: re-running drops the freshly-added constraint first. Each new CHECK
-- is a superset of the old one, so existing rows never violate it.

-- 1. status gains 'screen' (recruiter / phone screen, between applied and interview).
ALTER TABLE web.job_review DROP CONSTRAINT IF EXISTS applications_status_check;
ALTER TABLE web.job_review DROP CONSTRAINT IF EXISTS job_review_status_check;
ALTER TABLE web.job_review
    ADD CONSTRAINT job_review_status_check
    CHECK (status IN ('applied', 'skipped', 'screen', 'interview'));

-- 2. terminal-outcome axis.
ALTER TABLE web.job_review ADD COLUMN IF NOT EXISTS final_status text;
ALTER TABLE web.job_review ADD COLUMN IF NOT EXISTS final_at     timestamptz;
ALTER TABLE web.job_review DROP CONSTRAINT IF EXISTS job_review_final_status_check;
ALTER TABLE web.job_review
    ADD CONSTRAINT job_review_final_status_check
    CHECK (final_status IS NULL OR final_status IN ('rejected', 'offer'));

CREATE INDEX IF NOT EXISTS job_review_profile_final_idx
    ON web.job_review (sys_profile, final_status);

-- 3. typed events: the log records the actual transition, not a generic marker.
--    The list is a superset of the constraint widened by 006/009/011 (résumé +
--    cover-letter events) so existing rows stay valid, plus the new typed
--    review transitions. 'status_changed' is the pre-014 review marker, kept for
--    historical rows.
ALTER TABLE web.application_events DROP CONSTRAINT IF EXISTS application_events_event_type_check;
ALTER TABLE web.application_events
    ADD CONSTRAINT application_events_event_type_check
    CHECK (event_type IN (
        -- review: new typed transitions (+ legacy generic marker)
        'status_changed',
        'applied', 'skipped', 'screen', 'interview', 'rejected', 'offer',
        -- viewing / résumé / cover-letter events (from 002, 006, 009, 011)
        'viewed', 'resume_drafted', 'resume_finalized', 'resume_generated',
        'resume_template_saved', 'cover_letter_drafted', 'cover_letter_saved'
    ));
