-- +goose Up
-- +goose StatementBegin
CREATE TABLE balance_change_logs (
    id            BIGSERIAL        PRIMARY KEY,
    team_id       BIGINT           NOT NULL,
    for_team_id   BIGINT           NOT NULL,
    change_type   INTEGER          NOT NULL DEFAULT 0,
    change_amount DOUBLE PRECISION NOT NULL,
    balance_type  INTEGER          NOT NULL DEFAULT 0,
    balance       DOUBLE PRECISION NOT NULL,
    note          TEXT,
    created_by_id BIGINT           NOT NULL,
    created_at    TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_balance_change_logs_team_id     ON balance_change_logs (team_id);
CREATE INDEX idx_balance_change_logs_for_team_id ON balance_change_logs (for_team_id);
CREATE INDEX idx_balance_change_logs_created_at  ON balance_change_logs (created_at);

CREATE TABLE team_balances (
    id                     BIGSERIAL        PRIMARY KEY,
    team_id                BIGINT           NOT NULL,
    for_team_id            BIGINT           NOT NULL,
    balance_type           INTEGER          NOT NULL DEFAULT 0,
    balance                DOUBLE PRECISION NOT NULL,
    pending_payment_amount DOUBLE PRECISION NOT NULL,
    created_at             TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    CONSTRAINT uniq_team_balances UNIQUE (team_id, for_team_id, balance_type)
);
CREATE INDEX idx_team_balances_team_id     ON team_balances (team_id);
CREATE INDEX idx_team_balances_for_team_id ON team_balances (for_team_id);

CREATE TABLE team_balance_daily_logs (
    id            BIGSERIAL        PRIMARY KEY,
    day           TIMESTAMPTZ      NOT NULL,
    team_id       BIGINT           NOT NULL,
    for_team_id   BIGINT           NOT NULL,
    balance_type  INTEGER          NOT NULL DEFAULT 0,
    start_balance DOUBLE PRECISION NOT NULL,
    end_balance   DOUBLE PRECISION NOT NULL,
    change_amount DOUBLE PRECISION NOT NULL,
    updated_at    TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    created_at    TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    CONSTRAINT uniq_team_balance_daily_logs UNIQUE (day, team_id, for_team_id, balance_type)
);
CREATE INDEX idx_team_balance_daily_logs_day         ON team_balance_daily_logs (day);
CREATE INDEX idx_team_balance_daily_logs_team_id     ON team_balance_daily_logs (team_id);
CREATE INDEX idx_team_balance_daily_logs_for_team_id ON team_balance_daily_logs (for_team_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS team_balance_daily_logs;
DROP TABLE IF EXISTS team_balances;
DROP TABLE IF EXISTS balance_change_logs;
-- +goose StatementEnd
