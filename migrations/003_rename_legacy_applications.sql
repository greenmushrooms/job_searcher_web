-- Retire the prototype public.applications table by renaming, not dropping.
--
-- The data was forward-copied into web.applications by migration 001 and the
-- only known reader (the Flask dashboard's LEFT JOIN at dashboard_web.py:566)
-- has been repointed at web.applications. Rename rather than drop so that any
-- forgotten reader (a cron job, an ad-hoc script, a backup process) fails
-- loudly with "relation does not exist" instead of silently reading stale
-- data from a table nothing writes to anymore.
--
-- After a week of clean logs, a follow-up migration drops public.applications_legacy.

ALTER TABLE IF EXISTS public.applications RENAME TO applications_legacy;
