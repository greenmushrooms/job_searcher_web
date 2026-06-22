-- Slice A (AI rewrite): a generated resume now stores the per-bullet final
-- text — manual edits plus accepted AI rewrites — and which template it started
-- from, so the PDF renders exactly what the user approved without re-deriving
-- the text from the canonical resume.
--
-- Guarded with IF NOT EXISTS for the no-psql migrate re-run path.

ALTER TABLE web.jobs_resume
    ADD COLUMN IF NOT EXISTS template_id text NOT NULL DEFAULT 'default';

ALTER TABLE web.jobs_resume
    ADD COLUMN IF NOT EXISTS bullets jsonb NOT NULL DEFAULT '[]'::jsonb;
