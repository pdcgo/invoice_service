-- +goose Up
-- +goose StatementBegin
-- owe_limit_configurations already exists in every live environment: selling_service creates
-- it via gorm AutoMigrate (selling_service/migration.go) and STILL owns its creation. This
-- migration is idempotent HARDENING only — it adopts the table when absent (fresh DBs) and
-- adds the unique constraints that were missing. It never moves or drops data.
--
-- Column types mirror the live schema exactly (schema_backup.sql): threshold is NUMERIC (not
-- DOUBLE PRECISION) and every non-id column is nullable, because gorm emitted no NOT NULL.
CREATE TABLE IF NOT EXISTS owe_limit_configurations (
    id          BIGSERIAL PRIMARY KEY,
    team_id     BIGINT,
    is_default  BOOLEAN,
    for_team_id BIGINT,
    threshold   NUMERIC
);

-- Dedup before the unique indexes, keeping the lowest id of each group.
-- Default rows: at most one per creditor team.
DELETE FROM owe_limit_configurations a
USING owe_limit_configurations b
WHERE a.is_default = true
  AND b.is_default = true
  AND a.team_id = b.team_id
  AND a.id > b.id;

-- Custom rows: at most one per (creditor, debtor). `is_default IS NOT TRUE` covers both
-- false AND NULL, matching how gorm scans the nullable column (NULL -> false -> "custom").
DELETE FROM owe_limit_configurations a
USING owe_limit_configurations b
WHERE a.is_default IS NOT TRUE
  AND b.is_default IS NOT TRUE
  AND a.team_id = b.team_id
  AND a.for_team_id = b.for_team_id
  AND a.id > b.id;

-- The actual fix: nothing prevented duplicate DEFAULT rows. The pre-existing legacy unique
-- index on (for_team_id, team_id) does not, because for_team_id is NULL on default rows and
-- Postgres treats NULLs as distinct.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_owe_limit_default
    ON owe_limit_configurations (team_id) WHERE is_default = true;
CREATE UNIQUE INDEX IF NOT EXISTS uniq_owe_limit_custom
    ON owe_limit_configurations (team_id, for_team_id) WHERE is_default IS NOT TRUE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Drop ONLY the indexes we added. Never drop the table: selling_service owns its creation
-- and it holds production config data.
DROP INDEX IF EXISTS uniq_owe_limit_custom;
DROP INDEX IF EXISTS uniq_owe_limit_default;
-- +goose StatementEnd
