-- +goose Up
-- +edge-collector data migration
ALTER TABLE acquisition_device ADD COLUMN external_id VARCHAR(128) NOT NULL DEFAULT '';
UPDATE acquisition_device SET external_id = 'device-' || id WHERE trim(external_id) = '';
CREATE UNIQUE INDEX uk_acquisition_device_external_id ON acquisition_device (external_id) WHERE deleted = 0;

-- +goose Down
DROP INDEX uk_acquisition_device_external_id;
ALTER TABLE acquisition_device DROP COLUMN external_id;
