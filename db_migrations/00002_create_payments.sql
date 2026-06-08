-- +goose Up
-- +goose StatementBegin
CREATE TABLE payments (
    id              BIGSERIAL        PRIMARY KEY,
    team_id         BIGINT           NOT NULL,
    for_team_id     BIGINT           NOT NULL,
    document_id     TEXT,
    amount          DOUBLE PRECISION NOT NULL,
    note            TEXT,
    status          INTEGER          NOT NULL DEFAULT 0,
    created_by_id   BIGINT           NOT NULL,
    completed_by_id BIGINT,
    created_at      TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    accepted_at     TIMESTAMPTZ,
    rejected_at     TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_payments_team_id     ON payments (team_id);
CREATE INDEX idx_payments_for_team_id ON payments (for_team_id);
CREATE INDEX idx_payments_status      ON payments (status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS payments;
-- +goose StatementEnd
