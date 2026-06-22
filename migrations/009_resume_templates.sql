-- Slice B: reusable resume templates ("variants") under a person. A template is
-- a curated selection of canonical bullets with optional per-bullet text
-- overrides. NULL override_text means "use the live canonical text" (the linked
-- behaviour) so canonical edits flow into the template; a non-null override is
-- a manual/AI-rewritten wording frozen on the template.
--
-- The virtual "Default" (full canonical pool) is not stored — it is synthesised
-- in code. Only user-created variants live here. Guarded for the no-psql path.

CREATE TABLE IF NOT EXISTS web.resume_templates (
    sys_profile  text        NOT NULL,
    template_id  text        NOT NULL,
    name         text        NOT NULL,
    is_default   boolean     NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT NOW(),
    updated_at   timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (sys_profile, template_id)
);

CREATE TABLE IF NOT EXISTS web.resume_template_bullets (
    sys_profile   text NOT NULL,
    template_id   text NOT NULL,
    role_id       text NOT NULL,
    bullet_id     text NOT NULL,
    override_text text,
    sort_order    int  NOT NULL DEFAULT 0,
    PRIMARY KEY (sys_profile, template_id, role_id, bullet_id),
    FOREIGN KEY (sys_profile, template_id)
        REFERENCES web.resume_templates (sys_profile, template_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS resume_template_bullets_tpl_idx
    ON web.resume_template_bullets (sys_profile, template_id, sort_order);

-- Allow the resume_template_saved audit event.
ALTER TABLE web.application_events
    DROP CONSTRAINT IF EXISTS application_events_event_type_check;
ALTER TABLE web.application_events
    ADD CONSTRAINT application_events_event_type_check
    CHECK (event_type IN (
        'status_changed', 'viewed', 'resume_drafted',
        'resume_finalized', 'resume_generated', 'resume_template_saved'
    ));
