-- +goose Up
-- +goose StatementBegin
CREATE TABLE balance_change_order_sources (
    balance_change_log_id BIGINT      PRIMARY KEY,
    order_system          INTEGER     NOT NULL DEFAULT 0,
    order_id              BIGINT      NOT NULL,
    team_id               BIGINT      NOT NULL,
    warehouse_id          BIGINT      NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_balance_change_order_sources_log
        FOREIGN KEY (balance_change_log_id) REFERENCES balance_change_logs (id)
);
CREATE INDEX idx_balance_change_order_sources_order     ON balance_change_order_sources (order_id, order_system);
CREATE INDEX idx_balance_change_order_sources_warehouse ON balance_change_order_sources (warehouse_id, created_at);
CREATE INDEX idx_balance_change_order_sources_team      ON balance_change_order_sources (team_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS balance_change_order_sources;
-- +goose StatementEnd
