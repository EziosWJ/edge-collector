-- +goose Up
ALTER TABLE mqtt_command_journal ADD COLUMN command_payload TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE mqtt_command_journal DROP COLUMN command_payload;
