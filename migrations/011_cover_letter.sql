-- Cover letters: one per (job, profile), AI-drafted then hand-edited.
-- Separate table from web.jobs_resume so a letter can exist before/without a
-- saved résumé. Idempotent for the cmd/migrate re-run path.

CREATE TABLE IF NOT EXISTS web.jobs_cover_letter (
    job_id      text        NOT NULL,
    sys_profile text        NOT NULL,
    body        text        NOT NULL,
    model       text        NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (job_id, sys_profile)
);

-- Allow the cover_letter_drafted / cover_letter_saved audit events.
ALTER TABLE web.application_events
    DROP CONSTRAINT IF EXISTS application_events_event_type_check;
ALTER TABLE web.application_events
    ADD CONSTRAINT application_events_event_type_check
    CHECK (event_type IN (
        'status_changed', 'viewed', 'resume_drafted',
        'resume_finalized', 'resume_generated', 'resume_template_saved',
        'cover_letter_drafted', 'cover_letter_saved'
    ));
