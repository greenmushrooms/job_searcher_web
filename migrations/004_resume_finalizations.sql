-- Captures the user's final bullet selection for a tailored resume per (job_id, sys_profile).
--
-- Stored separately from web.application_events because finalizations need
-- random-access reads ("what did I send for this job?") and a fast unique key
-- per job, whereas events are an append-only log. We still write a
-- resume_finalized event for the audit trail.
--
-- kept_bullet_ids holds composite "role_id.bullet_id" strings — matches the
-- contract the DeepSeek prompt and response use.
--
-- resume_version pins the snapshot of the bullet pool we tailored against
-- (schema_version + content hash). When the resume content changes, the
-- version changes, and we can tell which finalizations are stale vs current.

CREATE TABLE IF NOT EXISTS web.resume_finalizations (
    job_id           text        NOT NULL,
    sys_profile      text        NOT NULL,
    resume_version   text        NOT NULL,
    kept_bullet_ids  text[]      NOT NULL,
    finalized_at     timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (job_id, sys_profile)
);

CREATE INDEX IF NOT EXISTS resume_finalizations_profile_time_idx
    ON web.resume_finalizations (sys_profile, finalized_at DESC);
