-- +goose Up
ALTER TABLE acquisition_channel
    ADD COLUMN inter_request_delay_ms INTEGER NOT NULL DEFAULT 0
        CHECK (inter_request_delay_ms BETWEEN 0 AND 60000);

-- +goose Down
ALTER TABLE acquisition_channel DROP COLUMN inter_request_delay_ms;
