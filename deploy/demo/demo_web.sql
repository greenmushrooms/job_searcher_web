-- Seeds the 'Demo' profile's web.* state for the public sample instance: a
-- fully synthetic persona (Alex Sample — no real person's data), a spread of
-- application statuses so the funnel/stats read well, and a couple of search
-- entries so /settings isn't empty.
--
-- Requires demo_public.sql first (reviews pick from the copied jobs). Run on
-- the box (owner of web.*) and on the homelab hub_db for local dev testing.
-- Idempotent: each block no-ops if Demo rows exist.

INSERT INTO web.user_profile (sys_profile, name, email, phone, github, location, summary, schema_version)
VALUES ('Demo', 'Alex Sample', 'alex.sample@example.com', '416-555-0199',
        'github.com/alexsample', 'Toronto, ON',
        'Data engineer with 8 years building batch and streaming pipelines on cloud warehouses; comfortable owning platforms end to end.', 2)
ON CONFLICT (sys_profile) DO NOTHING;

INSERT INTO web.resume_roles (sys_profile, role_id, title, company, location, dates, sort_order)
SELECT * FROM (VALUES
  ('Demo', 'northwind_analytics', 'Senior Data Engineer', 'Northwind Analytics', 'Toronto, ON', '2021 - Present', 0),
  ('Demo', 'acme_logistics', 'Data Engineer', 'Acme Logistics', 'Mississauga, ON', '2018 - 2021', 1)
) v(sys_profile, role_id, title, company, location, dates, sort_order)
WHERE NOT EXISTS (SELECT 1 FROM web.resume_roles WHERE sys_profile = 'Demo');

INSERT INTO web.resume_bullets (sys_profile, role_id, bullet_id, text, tags, sort_order)
SELECT * FROM (VALUES
  ('Demo', 'northwind_analytics', 'b1', 'Built a dbt + Airflow ELT platform loading 40+ sources into Snowflake, cutting nightly batch time from 6h to 90min', '{}'::text[], 0),
  ('Demo', 'northwind_analytics', 'b2', 'Introduced data contracts and CI checks that cut schema-related incidents by half', '{}'::text[], 1),
  ('Demo', 'northwind_analytics', 'b3', 'Mentored two junior engineers through their first production services', '{}'::text[], 2),
  ('Demo', 'acme_logistics', 'b1', 'Migrated on-prem SQL Server reporting to BigQuery with zero downtime cutover', '{}'::text[], 0),
  ('Demo', 'acme_logistics', 'b2', 'Streamed telematics events through Pub/Sub + Dataflow for near-real-time fleet dashboards', '{}'::text[], 1)
) v(sys_profile, role_id, bullet_id, text, tags, sort_order)
WHERE NOT EXISTS (SELECT 1 FROM web.resume_bullets WHERE sys_profile = 'Demo');

INSERT INTO web.resume_skills (sys_profile, skill_id, text, category, sort_order)
SELECT * FROM (VALUES
  ('Demo', 's1', 'SQL, Python, dbt, Airflow', 'Core', 0),
  ('Demo', 's2', 'Snowflake, BigQuery, Postgres', 'Warehouses', 1),
  ('Demo', 's3', 'GCP, Terraform, Docker', 'Platform', 2)
) v(sys_profile, skill_id, text, category, sort_order)
WHERE NOT EXISTS (SELECT 1 FROM web.resume_skills WHERE sys_profile = 'Demo');

INSERT INTO web.resume_education (sys_profile, education_id, degree, institution, location, sort_order)
SELECT 'Demo', 'edu0', 'B.Sc. Computer Science', 'University of Waterloo', 'Waterloo, ON', 0
WHERE NOT EXISTS (SELECT 1 FROM web.resume_education WHERE sys_profile = 'Demo');

INSERT INTO web.resume_master (sys_profile, markdown)
SELECT 'Demo', E'# ALEX SAMPLE\nalex.sample@example.com · 416-555-0199 · github.com/alexsample · Toronto, ON\n\n## Summary\nData engineer with 8 years building batch and streaming pipelines on cloud warehouses; comfortable owning platforms end to end.\n\n## Experience\n\n### Senior Data Engineer — Northwind Analytics (2021 - Present)\n- Built a dbt + Airflow ELT platform loading 40+ sources into Snowflake, cutting nightly batch time from 6h to 90min\n- Introduced data contracts and CI checks that cut schema-related incidents by half\n- Mentored two junior engineers through their first production services\n\n### Data Engineer — Acme Logistics (2018 - 2021)\n- Migrated on-prem SQL Server reporting to BigQuery with zero downtime cutover\n- Streamed telematics events through Pub/Sub + Dataflow for near-real-time fleet dashboards\n\n## Skills\n- Core: SQL, Python, dbt, Airflow\n- Warehouses: Snowflake, BigQuery, Postgres\n- Platform: GCP, Terraform, Docker\n\n## Education\nB.Sc. Computer Science — University of Waterloo\n'
WHERE NOT EXISTS (SELECT 1 FROM web.resume_master WHERE sys_profile = 'Demo');

-- Review spread over the best-scored demo jobs: interviews at the top, a
-- ghosted-looking tail (applied 30+ days ago, no outcome), one offer, two
-- rejections, some skips — so the funnel and stats pages demo every state.
WITH ranked AS (
  SELECT id, row_number() OVER (ORDER BY score DESC NULLS LAST, id) AS rn
  FROM (
    SELECT DISTINCT ON (j.id) j.id, e.avg_score AS score
    FROM public.jobspy_jobs j
    LEFT JOIN public.evaluated_jobs e ON e.job_id = j.id AND e.sys_profile = 'Demo'
    WHERE j.sys_profile = 'Demo'
    ORDER BY j.id, e.avg_score DESC
  ) d
  LIMIT 15
)
INSERT INTO web.job_review (job_id, sys_profile, status, final_status, final_at, notes, created_at, updated_at)
SELECT id, 'Demo',
  CASE WHEN rn <= 2 THEN 'interview'
       WHEN rn <= 4 THEN 'screen'
       WHEN rn <= 12 THEN 'applied'
       ELSE 'skipped' END,
  CASE WHEN rn = 5 THEN 'offer' WHEN rn IN (6, 7) THEN 'rejected' END,
  CASE WHEN rn IN (5, 6, 7) THEN now() - (rn || ' days')::interval END,
  CASE WHEN rn = 1 THEN 'panel round scheduled' END,
  now() - ((rn * 2) || ' days')::interval,
  now() - (rn || ' days')::interval
FROM ranked
WHERE NOT EXISTS (SELECT 1 FROM web.job_review WHERE sys_profile = 'Demo');

INSERT INTO web.job_searches (sys_profile, sort_order, title, location, searches)
SELECT * FROM (VALUES
  ('Demo', 0, 'data engineer', 'Toronto, ON', 30),
  ('Demo', 1, 'analytics engineer', 'Remote', 20)
) v(sys_profile, sort_order, title, location, searches)
WHERE NOT EXISTS (SELECT 1 FROM web.job_searches WHERE sys_profile = 'Demo');
