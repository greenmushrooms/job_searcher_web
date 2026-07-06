-- Per-entry search rows: each row is one (title, location, results) scrape —
-- replacing the titles[] x locations[] cross-product model of migration 015's
-- web.job_search_config. A profile's daily volume is now simply
-- sum(searches), capped at 300 by the app on save (and re-guarded by the
-- box-db-sync pull into adm.job_searches).
--
-- The old web.job_search_config stays in place (unreferenced) until the whole
-- chain has been on rows for a while; drop it in a later migration.
--
-- Idempotent: the seed only fires for profiles with no rows here yet, expanding
-- the old cross product so the scrape set is preserved exactly.
CREATE TABLE IF NOT EXISTS web.job_searches (
    sys_profile text NOT NULL,
    sort_order  int  NOT NULL,
    title       text NOT NULL,
    location    text NOT NULL,
    searches    int  NOT NULL DEFAULT 20 CHECK (searches >= 1),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (sys_profile, sort_order)
);

INSERT INTO web.job_searches (sys_profile, sort_order, title, location, searches)
SELECT c.sys_profile,
       row_number() OVER (PARTITION BY c.sys_profile ORDER BY t.ord, l.ord) - 1,
       t.title, l.location, c.searches
FROM web.job_search_config c
CROSS JOIN LATERAL unnest(c.titles)    WITH ORDINALITY AS t(title, ord)
CROSS JOIN LATERAL unnest(c.locations) WITH ORDINALITY AS l(location, ord)
WHERE NOT EXISTS (SELECT 1 FROM web.job_searches s WHERE s.sys_profile = c.sys_profile);
