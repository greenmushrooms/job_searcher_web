-- Refreshes the synthetic 'Demo' profile's pipeline data as a FULL mirror of
-- Slava's scrape/eval history (re-keyed with demo- ids) — so the public sample
-- instance shows the real pipeline's scale and cadence, not a stale snapshot.
--
-- Runs daily on the HOMELAB hub_db as part of the box-db-sync morning flow
-- (before the dump, so the box mirror carries the fresh copy). Safe to run
-- any time; replaces all Demo rows.
--
-- Why a full copy is safe:
--   * postings are public data; web.* (statuses, résumé) stays synthetic
--   * 'Demo' has no adm.resume row -> the pipeline never scrapes, extracts,
--     queues, or notifies for it, even though recent rows enter the 7-day
--     silver views
BEGIN;

DELETE FROM public.evaluated_jobs WHERE sys_profile = 'Demo';
DELETE FROM public.jobspy_jobs    WHERE sys_profile = 'Demo';

CREATE TEMP TABLE tj ON COMMIT DROP AS
    SELECT DISTINCT ON (id) * FROM public.jobspy_jobs
    WHERE sys_profile = 'Slava'
    ORDER BY id;
UPDATE tj SET sys_profile = 'Demo', id = 'demo-' || id;
INSERT INTO public.jobspy_jobs SELECT * FROM tj;

CREATE TEMP TABLE te ON COMMIT DROP AS
    SELECT e.* FROM public.evaluated_jobs e
    WHERE e.sys_profile = 'Slava';
UPDATE te SET sys_profile = 'Demo', job_id = 'demo-' || job_id,
              notified_at = NULL, sys_run_name = 'demo-mirror';
INSERT INTO public.evaluated_jobs SELECT * FROM te;

COMMIT;
