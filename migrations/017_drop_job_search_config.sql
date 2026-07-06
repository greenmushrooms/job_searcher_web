-- web.job_search_config (migration 015's titles[] x locations[] cross-product)
-- was superseded by per-entry web.job_searches (016) — every reader and writer
-- (the /settings page, box-db-sync) now uses job_searches, and keeping the
-- fossil invites the dual-source drift trap. 016's seed is guarded on this
-- table's existence, so the whole-directory re-run path stays intact.
-- (adm.job_search_config on the pipeline side is NOT this table — it stays,
-- owning the blocklists.)
DROP TABLE IF EXISTS web.job_search_config;
