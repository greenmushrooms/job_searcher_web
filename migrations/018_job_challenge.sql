-- Practice technical challenges: one per (job, profile), AI-generated from the
-- posting's stated requirements. Separate table from web.jobs_cover_letter for
-- the same reason that one is separate from web.jobs_resume — a challenge is
-- useful whether or not a résumé or letter exists for the job.
--
-- files/solution hold generated Python: `files` is what the candidate downloads
-- (stub + failing pytest suite, with a bug planted in one module), `solution` is
-- the reference implementation, kept server-side and revealed on request so the
-- exercise stays honest until you ask for it.
-- Idempotent for the cmd/migrate re-run path.

CREATE TABLE IF NOT EXISTS web.jobs_challenge (
    job_id         text        NOT NULL,
    sys_profile    text        NOT NULL,
    title          text        NOT NULL,
    brief          text        NOT NULL,
    skills         text[]      NOT NULL DEFAULT '{}',
    minutes        int         NOT NULL DEFAULT 30,
    files          jsonb       NOT NULL DEFAULT '[]',
    solution       jsonb       NOT NULL DEFAULT '[]',
    model          text        NOT NULL DEFAULT '',
    prompt_version text        NOT NULL DEFAULT '',
    updated_at     timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (job_id, sys_profile)
);

-- Allow the challenge_drafted / challenge_saved audit events.
ALTER TABLE web.application_events
    DROP CONSTRAINT IF EXISTS application_events_event_type_check;
ALTER TABLE web.application_events
    ADD CONSTRAINT application_events_event_type_check
    CHECK (event_type IN (
        'status_changed', 'applied', 'skipped', 'screen', 'interview',
        'rejected', 'offer', 'viewed', 'resume_drafted',
        'resume_finalized', 'resume_generated', 'resume_template_saved',
        'cover_letter_drafted', 'cover_letter_saved',
        'challenge_drafted', 'challenge_saved'
    ));
