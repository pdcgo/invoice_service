-- +goose Up
-- +goose StatementBegin
ALTER TABLE balance_change_order_sources ADD COLUMN order_item_id BIGINT;

CREATE TABLE balance_change_restock_sources (
    balance_change_log_id BIGINT      PRIMARY KEY,
    tx_id                 BIGINT      NOT NULL,
    tx_item_id            BIGINT,
    team_id               BIGINT      NOT NULL,
    warehouse_id          BIGINT      NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_balance_change_restock_sources_log
        FOREIGN KEY (balance_change_log_id) REFERENCES balance_change_logs (id)
);
CREATE INDEX idx_balance_change_restock_sources_tx        ON balance_change_restock_sources (tx_id);
CREATE INDEX idx_balance_change_restock_sources_warehouse ON balance_change_restock_sources (warehouse_id, created_at);
CREATE INDEX idx_balance_change_restock_sources_team      ON balance_change_restock_sources (team_id, created_at);

CREATE TABLE balance_change_broken_sources (
    balance_change_log_id BIGINT      PRIMARY KEY,
    tx_id                 BIGINT      NOT NULL,
    tx_item_id            BIGINT,
    team_id               BIGINT      NOT NULL,
    warehouse_id          BIGINT      NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_balance_change_broken_sources_log
        FOREIGN KEY (balance_change_log_id) REFERENCES balance_change_logs (id)
);
CREATE INDEX idx_balance_change_broken_sources_tx        ON balance_change_broken_sources (tx_id);
CREATE INDEX idx_balance_change_broken_sources_warehouse ON balance_change_broken_sources (warehouse_id, created_at);
CREATE INDEX idx_balance_change_broken_sources_team      ON balance_change_broken_sources (team_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS balance_change_broken_sources;
DROP TABLE IF EXISTS balance_change_restock_sources;
ALTER TABLE balance_change_order_sources DROP COLUMN IF EXISTS order_item_id;
-- +goose StatementEnd
