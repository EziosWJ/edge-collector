-- +goose Up
-- +edge-collector data migration
ALTER TABLE acquisition_channel
    ADD COLUMN protocol VARCHAR(32) NOT NULL DEFAULT 'MODBUS_RTU';

ALTER TABLE acquisition_channel
    ADD CONSTRAINT ck_acquisition_channel_protocol
    CHECK (protocol IN ('MODBUS_RTU', 'MODBUS_TCP', 'MODBUS_UDP', 'MODBUS_RTU_OVER_UDP'));

CREATE TABLE acquisition_serial_channel (
    channel_id BIGINT PRIMARY KEY,
    port VARCHAR(255) NOT NULL,
    baud_rate INTEGER NOT NULL,
    data_bits SMALLINT NOT NULL,
    stop_bits SMALLINT NOT NULL,
    parity VARCHAR(1) NOT NULL,
    create_time TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_acquisition_serial_channel_channel FOREIGN KEY (channel_id) REFERENCES acquisition_channel (id) ON DELETE CASCADE,
    CONSTRAINT ck_acquisition_serial_channel_port_not_blank CHECK (length(trim(port)) > 0),
    CONSTRAINT ck_acquisition_serial_channel_baud_rate CHECK (baud_rate > 0),
    CONSTRAINT ck_acquisition_serial_channel_data_bits CHECK (data_bits IN (7, 8)),
    CONSTRAINT ck_acquisition_serial_channel_stop_bits CHECK (stop_bits IN (1, 2)),
    CONSTRAINT ck_acquisition_serial_channel_parity CHECK (parity IN ('N', 'E', 'O'))
);

WITH legacy_serial AS (
    SELECT id, port, baud_rate, data_bits, stop_bits, parity
    FROM acquisition_channel
)
INSERT INTO acquisition_serial_channel (channel_id, port, baud_rate, data_bits, stop_bits, parity)
SELECT id, port, baud_rate, data_bits, stop_bits, parity
FROM legacy_serial;

ALTER TABLE acquisition_channel
    DROP CONSTRAINT ck_acquisition_channel_port_not_blank,
    DROP CONSTRAINT ck_acquisition_channel_baud_rate,
    DROP CONSTRAINT ck_acquisition_channel_data_bits,
    DROP CONSTRAINT ck_acquisition_channel_stop_bits,
    DROP CONSTRAINT ck_acquisition_channel_parity,
    DROP COLUMN port,
    DROP COLUMN baud_rate,
    DROP COLUMN data_bits,
    DROP COLUMN stop_bits,
    DROP COLUMN parity;

DROP INDEX IF EXISTS uk_acquisition_device_channel_slave;
ALTER TABLE acquisition_device DROP CONSTRAINT ck_acquisition_device_slave_id;
ALTER TABLE acquisition_device RENAME COLUMN slave_id TO unit_id;
ALTER TABLE acquisition_device
    ADD CONSTRAINT ck_acquisition_device_unit_id CHECK (unit_id BETWEEN 0 AND 255);

CREATE TABLE acquisition_network_device (
    device_id BIGINT PRIMARY KEY,
    host VARCHAR(255) NOT NULL,
    port INTEGER NOT NULL,
    create_time TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_acquisition_network_device_device FOREIGN KEY (device_id) REFERENCES acquisition_device (id) ON DELETE CASCADE,
    CONSTRAINT ck_acquisition_network_device_host_not_blank CHECK (length(trim(host)) > 0),
    CONSTRAINT ck_acquisition_network_device_port CHECK (port BETWEEN 1 AND 65535)
);

-- +goose Down
DO $$
BEGIN
    IF EXISTS (
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
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot roll back acquisition transport migration: network configuration or non-legacy Unit ID is present';
    END IF;
END $$;

DROP TABLE IF EXISTS acquisition_network_device;

ALTER TABLE acquisition_device DROP CONSTRAINT ck_acquisition_device_unit_id;
ALTER TABLE acquisition_device RENAME COLUMN unit_id TO slave_id;
ALTER TABLE acquisition_device
    ADD CONSTRAINT ck_acquisition_device_slave_id CHECK (slave_id BETWEEN 1 AND 247);
CREATE UNIQUE INDEX uk_acquisition_device_channel_slave ON acquisition_device (channel_id, slave_id) WHERE deleted = 0;

ALTER TABLE acquisition_channel
    ADD COLUMN port VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN baud_rate INTEGER NOT NULL DEFAULT 19200,
    ADD COLUMN data_bits SMALLINT NOT NULL DEFAULT 8,
    ADD COLUMN stop_bits SMALLINT NOT NULL DEFAULT 2,
    ADD COLUMN parity VARCHAR(1) NOT NULL DEFAULT 'N',
    ADD CONSTRAINT ck_acquisition_channel_port_not_blank CHECK (length(trim(port)) > 0),
    ADD CONSTRAINT ck_acquisition_channel_baud_rate CHECK (baud_rate > 0),
    ADD CONSTRAINT ck_acquisition_channel_data_bits CHECK (data_bits IN (7, 8)),
    ADD CONSTRAINT ck_acquisition_channel_stop_bits CHECK (stop_bits IN (1, 2)),
    ADD CONSTRAINT ck_acquisition_channel_parity CHECK (parity IN ('N', 'E', 'O'));

WITH legacy_serial AS (
    SELECT channel_id, port, baud_rate, data_bits, stop_bits, parity
    FROM acquisition_serial_channel
)
UPDATE acquisition_channel AS channel
SET (port, baud_rate, data_bits, stop_bits, parity) = (
    SELECT serial.port, serial.baud_rate, serial.data_bits, serial.stop_bits, serial.parity
    FROM legacy_serial AS serial
    WHERE serial.channel_id = channel.id
);

DROP TABLE IF EXISTS acquisition_serial_channel;
ALTER TABLE acquisition_channel DROP CONSTRAINT ck_acquisition_channel_protocol;
ALTER TABLE acquisition_channel DROP COLUMN protocol;
