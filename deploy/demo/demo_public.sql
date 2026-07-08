-- Seeds the synthetic 'Demo' profile's pipeline data for the public read-only
-- sample instance (sample.jobs.*): copies ~250 already-public job postings
-- (and their eval rows) from Slava's history, re-keyed as sys_profile='Demo'
-- with 'demo-' id prefixes.
--
-- Run on the HOMELAB hub_db (the sync source — box public.* is truncated and
-- re-mirrored daily, so Demo rows must live here to survive) and once directly
-- on the box for immediate availability.
--
-- The 8..60-day-old window keeps the copies out of jobs_for_extract's 7-day
-- silver view, and 'Demo' has no adm.resume row, so the pipeline never
-- scrapes, extracts, or notifies for it. Idempotent: no-op when Demo rows exist.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.jobspy_jobs WHERE sys_profile = 'Demo') THEN
        RAISE NOTICE 'Demo rows already present - skipping';
        RETURN;
    END IF;

    CREATE TEMP TABLE tj AS
        SELECT * FROM (
            SELECT DISTINCT ON (id) *
            FROM public.jobspy_jobs
            WHERE sys_profile = 'Slava'
              AND date_posted BETWEEN CURRENT_DATE - 60 AND CURRENT_DATE - 8
            ORDER BY id
        ) d
        ORDER BY date_posted DESC
        LIMIT 250;

    CREATE TEMP TABLE te AS
        SELECT DISTINCT ON (e.job_id) e.*
        FROM public.evaluated_jobs e
        JOIN tj ON tj.id = e.job_id
        WHERE e.sys_profile = 'Slava'
        ORDER BY e.job_id, e.avg_score DESC;

    UPDATE tj SET sys_profile = 'Demo', id = 'demo-' || id;
    UPDATE te SET sys_profile = 'Demo', job_id = 'demo-' || job_id,
                  notified_at = NULL, sys_run_name = 'demo-seed';

    INSERT INTO public.jobspy_jobs SELECT * FROM tj;
    INSERT INTO public.evaluated_jobs SELECT * FROM te;
    RAISE NOTICE 'seeded % demo jobs', (SELECT count(*) FROM tj);
END $$;
