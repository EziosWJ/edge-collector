-- +goose NO TRANSACTION
-- +goose Up
-- +edge-collector data migration
PRAGMA foreign_keys = OFF;

CREATE TABLE acquisition_serial_channel (
    channel_id INTEGER PRIMARY KEY,
    port VARCHAR(255) NOT NULL,
    baud_rate INTEGER NOT NULL,
    data_bits INTEGER NOT NULL,
    stop_bits INTEGER NOT NULL,
    parity VARCHAR(1) NOT NULL,
    create_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_acquisition_serial_channel_channel FOREIGN KEY (channel_id) REFERENCES acquisition_channel (id) ON DELETE CASCADE,
    CONSTRAINT ck_acquisition_serial_channel_port_not_blank CHECK (length(trim(port)) > 0),
    CONSTRAINT ck_acquisition_serial_channel_baud_rate CHECK (baud_rate > 0),
    CONSTRAINT ck_acquisition_serial_channel_data_bits CHECK (data_bits IN (7, 8)),
    CONSTRAINT ck_acquisition_serial_channel_stop_bits CHECK (stop_bits IN (1, 2)),
    CONSTRAINT ck_acquisition_serial_channel_parity CHECK (parity IN ('N', 'E', 'O'))
);

WITH legacy_serial AS (
    SELECT id, port, baud_rate, data_bits, stop_bits, parity, create_time, update_time
    FROM acquisition_channel
)
INSERT INTO acquisition_serial_channel (channel_id, port, baud_rate, data_bits, stop_bits, parity, create_time, update_time)
SELECT id, port, baud_rate, data_bits, stop_bits, parity, create_time, update_time
FROM legacy_serial;

DROP INDEX IF EXISTS uk_acquisition_device_channel_slave;
DROP INDEX IF EXISTS idx_acquisition_channel_enabled;

CREATE TABLE acquisition_channel_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(100) NOT NULL,
    protocol VARCHAR(32) NOT NULL DEFAULT 'MODBUS_RTU',
    timeout_ms INTEGER NOT NULL DEFAULT 300,
    enabled INTEGER NOT NULL DEFAULT 1,
    create_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted INTEGER NOT NULL DEFAULT 0,
    inter_request_delay_ms INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT ck_acquisition_channel_name_not_blank CHECK (length(trim(name)) > 0),
    CONSTRAINT ck_acquisition_channel_protocol CHECK (protocol IN ('MODBUS_RTU', 'MODBUS_TCP', 'MODBUS_UDP', 'MODBUS_RTU_OVER_UDP')),
    CONSTRAINT ck_acquisition_channel_timeout CHECK (timeout_ms > 0),
    CONSTRAINT ck_acquisition_channel_enabled CHECK (enabled IN (0, 1)),
    CONSTRAINT ck_acquisition_channel_deleted CHECK (deleted IN (0, 1)),
    CONSTRAINT ck_acquisition_channel_inter_request_delay CHECK (inter_request_delay_ms BETWEEN 0 AND 60000)
);

WITH legacy_channel AS (
    SELECT id, name, timeout_ms, enabled, create_time, update_time, deleted, inter_request_delay_ms
    FROM acquisition_channel
)
INSERT INTO acquisition_channel_new (id, name, protocol, timeout_ms, enabled, create_time, update_time, deleted, inter_request_delay_ms)
SELECT id, name, 'MODBUS_RTU', timeout_ms, enabled, create_time, update_time, deleted, inter_request_delay_ms
FROM legacy_channel;

DROP TABLE acquisition_channel;
ALTER TABLE acquisition_channel_new RENAME TO acquisition_channel;
CREATE INDEX idx_acquisition_channel_enabled ON acquisition_channel (enabled, deleted, id);

DROP INDEX IF EXISTS idx_acquisition_device_channel;

CREATE TABLE acquisition_device_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(100) NOT NULL,
    device_type VARCHAR(50) NOT NULL,
    channel_id INTEGER NOT NULL,
    unit_id INTEGER NOT NULL,
    poll_interval_ms INTEGER NOT NULL DEFAULT 1000,
    failure_threshold INTEGER NOT NULL DEFAULT 3,
    enabled INTEGER NOT NULL DEFAULT 1,
    create_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT fk_acquisition_device_channel FOREIGN KEY (channel_id) REFERENCES acquisition_channel (id) ON DELETE RESTRICT,
    CONSTRAINT ck_acquisition_device_name_not_blank CHECK (length(trim(name)) > 0),
    CONSTRAINT ck_acquisition_device_type CHECK (device_type = 'FEED_PROTECTOR'),
    CONSTRAINT ck_acquisition_device_unit_id CHECK (unit_id BETWEEN 0 AND 255),
    CONSTRAINT ck_acquisition_device_poll_interval CHECK (poll_interval_ms > 0),
    CONSTRAINT ck_acquisition_device_failure_threshold CHECK (failure_threshold > 0),
    CONSTRAINT ck_acquisition_device_enabled CHECK (enabled IN (0, 1)),
    CONSTRAINT ck_acquisition_device_deleted CHECK (deleted IN (0, 1))
);

WITH legacy_device AS (
    SELECT id, name, device_type, channel_id, slave_id, poll_interval_ms, failure_threshold, enabled, create_time, update_time, deleted
    FROM acquisition_device
)
INSERT INTO acquisition_device_new (id, name, device_type, channel_id, unit_id, poll_interval_ms, failure_threshold, enabled, create_time, update_time, deleted)
SELECT id, name, device_type, channel_id, slave_id, poll_interval_ms, failure_threshold, enabled, create_time, update_time, deleted
FROM legacy_device;

DROP TABLE acquisition_device;
ALTER TABLE acquisition_device_new RENAME TO acquisition_device;
CREATE INDEX idx_acquisition_device_channel ON acquisition_device (channel_id, enabled, deleted, id);

CREATE TABLE acquisition_network_device (
    device_id INTEGER PRIMARY KEY,
    host VARCHAR(255) NOT NULL,
    port INTEGER NOT NULL,
    create_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_acquisition_network_device_device FOREIGN KEY (device_id) REFERENCES acquisition_device (id) ON DELETE CASCADE,
    CONSTRAINT ck_acquisition_network_device_host_not_blank CHECK (length(trim(host)) > 0),
    CONSTRAINT ck_acquisition_network_device_port CHECK (port BETWEEN 1 AND 65535)
);

PRAGMA foreign_keys = ON;

-- +goose Down
DROP TRIGGER IF EXISTS acquisition_transport_rollback_guard_channel;
-- +goose StatementBegin
CREATE TEMP TRIGGER acquisition_transport_rollback_guard_channel
BEFORE UPDATE ON acquisition_channel
WHEN EXISTS (
    SELECT 1
    FROM acquisition_channel AS channel
    WHERE channel.protocol <> 'MODBUS_RTU'
) OR EXISTS (
    SELECT 1
    FROM acquisition_channel AS channel
    LEFT JOIN acquisition_serial_channel AS serial ON serial.channel_id = channel.id
    WHERE serial.channel_id IS NULL
) OR EXISTS (
    SELECT 1
    FROM acquisition_network_device
) OR EXISTS (
    SELECT 1
    FROM acquisition_device
    WHERE unit_id NOT BETWEEN 1 AND 247
) OR EXISTS (
    SELECT channel_id, unit_id
    FROM acquisition_device
    WHERE deleted = 0
    GROUP BY channel_id, unit_id
    HAVING count(*) > 1
)
BEGIN
    SELECT RAISE(ABORT, 'cannot roll back acquisition transport migration: network configuration or non-legacy Unit ID is present');
END;
-- +goose StatementEnd
UPDATE acquisition_channel SET id = id;
DROP TRIGGER acquisition_transport_rollback_guard_channel;

PRAGMA foreign_keys = OFF;
DROP TABLE IF EXISTS acquisition_network_device;
DROP INDEX IF EXISTS idx_acquisition_device_channel;

CREATE TABLE acquisition_device_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(100) NOT NULL,
    device_type VARCHAR(50) NOT NULL,
    channel_id INTEGER NOT NULL,
    slave_id INTEGER NOT NULL,
    poll_interval_ms INTEGER NOT NULL DEFAULT 1000,
    failure_threshold INTEGER NOT NULL DEFAULT 3,
    enabled INTEGER NOT NULL DEFAULT 1,
    create_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT fk_acquisition_device_channel_old FOREIGN KEY (channel_id) REFERENCES acquisition_channel (id) ON DELETE RESTRICT,
    CONSTRAINT ck_acquisition_device_name_not_blank_old CHECK (length(trim(name)) > 0),
    CONSTRAINT ck_acquisition_device_type_old CHECK (device_type = 'FEED_PROTECTOR'),
    CONSTRAINT ck_acquisition_device_slave_id_old CHECK (slave_id BETWEEN 1 AND 247),
    CONSTRAINT ck_acquisition_device_poll_interval_old CHECK (poll_interval_ms > 0),
    CONSTRAINT ck_acquisition_device_failure_threshold_old CHECK (failure_threshold > 0),
    CONSTRAINT ck_acquisition_device_enabled_old CHECK (enabled IN (0, 1)),
    CONSTRAINT ck_acquisition_device_deleted_old CHECK (deleted IN (0, 1))
);
WITH migrated_device AS (
    SELECT id, name, device_type, channel_id, unit_id, poll_interval_ms, failure_threshold, enabled, create_time, update_time, deleted
    FROM acquisition_device
)
INSERT INTO acquisition_device_old
SELECT id, name, device_type, channel_id, unit_id, poll_interval_ms, failure_threshold, enabled, create_time, update_time, deleted
FROM migrated_device;
DROP TABLE acquisition_device;
ALTER TABLE acquisition_device_old RENAME TO acquisition_device;
CREATE INDEX idx_acquisition_device_channel ON acquisition_device (channel_id, enabled, deleted, id);
CREATE UNIQUE INDEX uk_acquisition_device_channel_slave ON acquisition_device (channel_id, slave_id) WHERE deleted = 0;

DROP INDEX IF EXISTS idx_acquisition_channel_enabled;
CREATE TABLE acquisition_channel_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(100) NOT NULL,
    port VARCHAR(255) NOT NULL,
    baud_rate INTEGER NOT NULL DEFAULT 19200,
    data_bits INTEGER NOT NULL DEFAULT 8,
    stop_bits INTEGER NOT NULL DEFAULT 2,
    parity VARCHAR(1) NOT NULL DEFAULT 'N',
    timeout_ms INTEGER NOT NULL DEFAULT 300,
    enabled INTEGER NOT NULL DEFAULT 1,
    create_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted INTEGER NOT NULL DEFAULT 0,
    inter_request_delay_ms INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT ck_acquisition_channel_name_not_blank_old CHECK (length(trim(name)) > 0),
    CONSTRAINT ck_acquisition_channel_port_not_blank_old CHECK (length(trim(port)) > 0),
    CONSTRAINT ck_acquisition_channel_baud_rate_old CHECK (baud_rate > 0),
    CONSTRAINT ck_acquisition_channel_data_bits_old CHECK (data_bits IN (7, 8)),
    CONSTRAINT ck_acquisition_channel_stop_bits_old CHECK (stop_bits IN (1, 2)),
    CONSTRAINT ck_acquisition_channel_parity_old CHECK (parity IN ('N', 'E', 'O')),
    CONSTRAINT ck_acquisition_channel_timeout_old CHECK (timeout_ms > 0),
    CONSTRAINT ck_acquisition_channel_enabled_old CHECK (enabled IN (0, 1)),
    CONSTRAINT ck_acquisition_channel_deleted_old CHECK (deleted IN (0, 1)),
    CONSTRAINT ck_acquisition_channel_inter_request_delay_old CHECK (inter_request_delay_ms BETWEEN 0 AND 60000)
);
WITH migrated_channel AS (
    SELECT channel.id, channel.name, serial.port, serial.baud_rate, serial.data_bits, serial.stop_bits, serial.parity, channel.timeout_ms, channel.enabled, channel.create_time, channel.update_time, channel.deleted, channel.inter_request_delay_ms
    FROM acquisition_channel AS channel JOIN acquisition_serial_channel AS serial ON serial.channel_id = channel.id
)
INSERT INTO acquisition_channel_old (id, name, port, baud_rate, data_bits, stop_bits, parity, timeout_ms, enabled, create_time, update_time, deleted, inter_request_delay_ms)
SELECT channel.id, channel.name, channel.port, channel.baud_rate, channel.data_bits, channel.stop_bits, channel.parity, channel.timeout_ms, channel.enabled, channel.create_time, channel.update_time, channel.deleted, channel.inter_request_delay_ms
FROM migrated_channel AS channel;
DROP TABLE acquisition_channel;
ALTER TABLE acquisition_channel_old RENAME TO acquisition_channel;
CREATE INDEX idx_acquisition_channel_enabled ON acquisition_channel (enabled, deleted, id);
DROP TABLE IF EXISTS acquisition_serial_channel;
PRAGMA foreign_keys = ON;
