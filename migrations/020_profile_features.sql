-- Per-profile feature flags. Opt-IN by design: a profile with no row for a
-- feature does not get it, so shipping something half-finished to the shared
-- box can't surprise anyone who didn't ask for it.
--
-- Deliberately a table rather than a column on web.user_profile: flags come and
-- go, and a table lets one land without a schema change every time. There is no
-- toggle UI yet — this is SQL-managed, and lives on both the hub and the box
-- because neither side syncs it.
-- Idempotent for the cmd/migrate re-run path.

CREATE TABLE IF NOT EXISTS web.profile_features (
    sys_profile text        NOT NULL,
    feature     text        NOT NULL,
    enabled     boolean     NOT NULL DEFAULT true,
    updated_at  timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (sys_profile, feature)
);

-- The practice-exercise workspace section. Slava asked for it; everyone else
-- (including the public Demo, which can't POST anyway) stays opted out until
-- they say otherwise.
INSERT INTO web.profile_features (sys_profile, feature, enabled)
VALUES ('Slava', 'practice', true)
ON CONFLICT (sys_profile, feature) DO NOTHING;
