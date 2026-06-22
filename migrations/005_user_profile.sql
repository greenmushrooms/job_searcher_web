-- The canonical resume moves out of resume_htmx/resume_data.json and into the
-- DB. job_searcher_web becomes the owner/writer: the left-hand editor upserts
-- here, and resume.Load() reads from here instead of the flat file.
--
-- Keyed by sys_profile so each person in the pipeline (Slava, Cait, Kezia, Ray)
-- can own a distinct resume. Seed/import is handled by api/cmd/seed-resume,
-- which also applies this file (the deploy box has no psql).
--
-- Granularity: roles and bullets are individually addressable because the
-- left-hand editor edits one bullet at a time and the DeepSeek diff + the
-- right-hand checkbox form both operate per bullet (role_id.bullet_id — the
-- same composite contract used in web.resume_finalizations and the LLM prompt).
-- contact/summary/skills/education are coarser, edited as whole rows.

-- Singleton-per-profile: contact block + summary + schema version.
CREATE TABLE IF NOT EXISTS web.user_profile (
    sys_profile    text        PRIMARY KEY,
    name           text        NOT NULL DEFAULT '',
    email          text        NOT NULL DEFAULT '',
    phone          text        NOT NULL DEFAULT '',
    github         text        NOT NULL DEFAULT '',
    location       text        NOT NULL DEFAULT '',
    summary        text        NOT NULL DEFAULT '',
    schema_version int         NOT NULL DEFAULT 2,
    updated_at     timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS web.resume_skills (
    sys_profile text    NOT NULL,
    skill_id    text    NOT NULL,
    text        text    NOT NULL DEFAULT '',
    category    text    NOT NULL DEFAULT '',
    sort_order  int     NOT NULL DEFAULT 0,
    retired     boolean NOT NULL DEFAULT false,
    PRIMARY KEY (sys_profile, skill_id)
);

CREATE TABLE IF NOT EXISTS web.resume_roles (
    sys_profile text    NOT NULL,
    role_id     text    NOT NULL,
    title       text    NOT NULL DEFAULT '',
    company     text    NOT NULL DEFAULT '',
    location    text    NOT NULL DEFAULT '',
    dates       text    NOT NULL DEFAULT '',
    sort_order  int     NOT NULL DEFAULT 0,
    retired     boolean NOT NULL DEFAULT false,
    PRIMARY KEY (sys_profile, role_id)
);

CREATE TABLE IF NOT EXISTS web.resume_bullets (
    sys_profile text    NOT NULL,
    role_id     text    NOT NULL,
    bullet_id   text    NOT NULL,
    text        text    NOT NULL DEFAULT '',
    tags        text[]  NOT NULL DEFAULT '{}',
    sort_order  int     NOT NULL DEFAULT 0,
    retired     boolean NOT NULL DEFAULT false,
    PRIMARY KEY (sys_profile, role_id, bullet_id),
    FOREIGN KEY (sys_profile, role_id)
        REFERENCES web.resume_roles (sys_profile, role_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS web.resume_education (
    sys_profile  text NOT NULL,
    education_id text NOT NULL,
    degree       text NOT NULL DEFAULT '',
    institution  text NOT NULL DEFAULT '',
    location     text NOT NULL DEFAULT '',
    sort_order   int  NOT NULL DEFAULT 0,
    PRIMARY KEY (sys_profile, education_id)
);
