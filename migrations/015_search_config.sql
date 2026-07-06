-- Per-profile search criteria, editable from the web UI. This is the box-side
-- editing surface for the pipeline's adm.job_search_config (which the daily
-- load-jobs scrape reads on the homelab): the box-db-sync flow pulls this
-- table back to the homelab each morning and upserts titles/locations/searches
-- into adm.job_search_config (blocklists stay pipeline-owned).
--
-- The app clamps searches so titles x locations x searches <= 300 per run;
-- the sync re-enforces the cap when writing into adm as a second guard.
--
-- Idempotent for the cmd/migrate re-run path.
CREATE TABLE IF NOT EXISTS web.job_search_config (
    sys_profile text PRIMARY KEY,
    titles      text[] NOT NULL,
    locations   text[] NOT NULL,
    searches    integer NOT NULL DEFAULT 20 CHECK (searches >= 1),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
