-- Markdown-centric tailored-resume workflow.
--
-- The per-job tailored resume and the reusable templates become markdown
-- documents (free-form, user-editable). The canonical web.resume_* tables stay
-- as the structured source the DeepSeek draft and the initial markdown render
-- from, but the *saved* per-job result and the *saved* template body are now a
-- single markdown blob. Guarded for the no-psql migrate path.

ALTER TABLE web.jobs_resume      ADD COLUMN IF NOT EXISTS markdown text;
ALTER TABLE web.resume_templates ADD COLUMN IF NOT EXISTS markdown text;
