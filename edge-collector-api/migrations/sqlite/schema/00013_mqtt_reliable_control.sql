-- +goose Up
CREATE TABLE mqtt_config (
    id INTEGER PRIMARY KEY,
    enabled INTEGER NOT NULL DEFAULT 0,
    edge_id VARCHAR(128) NOT NULL,
    broker_url VARCHAR(2048) NOT NULL,
    protocol_version VARCHAR(16) NOT NULL DEFAULT 'MQTT_5',
    client_id VARCHAR(256) NOT NULL,
    username VARCHAR(256) NOT NULL DEFAULT '',
    password_ciphertext TEXT,
    tls_enabled INTEGER NOT NULL DEFAULT 0,
    ca_certificate TEXT NOT NULL DEFAULT '',
    client_certificate TEXT NOT NULL DEFAULT '',
    client_private_key_ciphertext TEXT,
    keep_alive_seconds INTEGER NOT NULL DEFAULT 30,
    connect_timeout_ms INTEGER NOT NULL DEFAULT 10000,
    reconnect_min_ms INTEGER NOT NULL DEFAULT 1000,
    reconnect_max_ms INTEGER NOT NULL DEFAULT 60000,
    topic_prefix VARCHAR(512) NOT NULL DEFAULT 'edge',
    raw_publish_interval_ms INTEGER NOT NULL DEFAULT 1000,
    outbox_max_rows INTEGER NOT NULL DEFAULT 10000,
    outbox_max_bytes INTEGER NOT NULL DEFAULT 67108864,
    outbox_retention_days INTEGER NOT NULL DEFAULT 7,
    command_journal_retention_days INTEGER NOT NULL DEFAULT 7,
    command_journal_max_rows INTEGER NOT NULL DEFAULT 10000,
    command_queue_capacity INTEGER NOT NULL DEFAULT 32,
    command_poll_fairness INTEGER NOT NULL DEFAULT 1,
    create_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_mqtt_config_enabled CHECK (enabled IN (0, 1)),
    CONSTRAINT ck_mqtt_config_tls_enabled CHECK (tls_enabled IN (0, 1)),
    CONSTRAINT ck_mqtt_config_protocol CHECK (protocol_version IN ('MQTT_5', 'MQTT_3_1_1')),
    CONSTRAINT ck_mqtt_config_keep_alive CHECK (keep_alive_seconds BETWEEN 5 AND 3600),
    CONSTRAINT ck_mqtt_config_connect_timeout CHECK (connect_timeout_ms BETWEEN 100 AND 120000),
    CONSTRAINT ck_mqtt_config_reconnect_min CHECK (reconnect_min_ms BETWEEN 100 AND 60000),
    CONSTRAINT ck_mqtt_config_reconnect_max CHECK (reconnect_max_ms BETWEEN reconnect_min_ms AND 600000),
    CONSTRAINT ck_mqtt_config_raw_interval CHECK (raw_publish_interval_ms BETWEEN 50 AND 3600000),
    CONSTRAINT ck_mqtt_config_outbox_rows CHECK (outbox_max_rows BETWEEN 1 AND 1000000),
    CONSTRAINT ck_mqtt_config_outbox_bytes CHECK (outbox_max_bytes BETWEEN 1024 AND 1073741824),
    CONSTRAINT ck_mqtt_config_outbox_retention CHECK (outbox_retention_days BETWEEN 1 AND 3650),
    CONSTRAINT ck_mqtt_config_journal_retention CHECK (command_journal_retention_days BETWEEN 1 AND 3650),
    CONSTRAINT ck_mqtt_config_journal_rows CHECK (command_journal_max_rows BETWEEN 1 AND 1000000),
    CONSTRAINT ck_mqtt_config_queue_capacity CHECK (command_queue_capacity BETWEEN 1 AND 100000),
    CONSTRAINT ck_mqtt_config_fairness CHECK (command_poll_fairness BETWEEN 1 AND 100)
);

CREATE TABLE mqtt_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id VARCHAR(128) NOT NULL UNIQUE,
    message_type VARCHAR(32) NOT NULL,
    command_id VARCHAR(128),
    topic VARCHAR(2048) NOT NULL,
    qos INTEGER NOT NULL,
    retain INTEGER NOT NULL DEFAULT 0,
    payload TEXT NOT NULL,
    payload_bytes INTEGER NOT NULL,
    priority INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    last_attempt_at DATETIME,
    last_error TEXT,
    CONSTRAINT ck_mqtt_outbox_type CHECK (message_type IN ('EVENT', 'COMMAND_ACCEPTED', 'COMMAND_RESULT')),
    CONSTRAINT ck_mqtt_outbox_qos CHECK (qos IN (0, 1)),
    CONSTRAINT ck_mqtt_outbox_retain CHECK (retain IN (0, 1)),
    CONSTRAINT ck_mqtt_outbox_payload_bytes CHECK (payload_bytes >= 0),
    CONSTRAINT ck_mqtt_outbox_attempt_count CHECK (attempt_count >= 0)
);
CREATE INDEX idx_mqtt_outbox_delivery ON mqtt_outbox (priority DESC, created_at, id);
CREATE INDEX idx_mqtt_outbox_expiry ON mqtt_outbox (expires_at);

CREATE TABLE mqtt_outbox_reservation (
    command_id VARCHAR(128) PRIMARY KEY,
    reserved_rows INTEGER NOT NULL,
    reserved_bytes INTEGER NOT NULL,
    accepted_bytes INTEGER NOT NULL,
    final_bytes INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_mqtt_reservation_rows CHECK (reserved_rows >= 1),
    CONSTRAINT ck_mqtt_reservation_bytes CHECK (reserved_bytes >= 0 AND accepted_bytes >= 0 AND final_bytes >= 0)
);

CREATE TABLE mqtt_command_journal (
    command_id VARCHAR(128) PRIMARY KEY,
    device_id VARCHAR(128) NOT NULL,
    command_name VARCHAR(128) NOT NULL,
    payload_hash CHAR(64) NOT NULL,
    received_at DATETIME NOT NULL,
    issued_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    status VARCHAR(16) NOT NULL,
    started_at DATETIME,
    completed_at DATETIME,
    result_payload TEXT NOT NULL DEFAULT '',
    error_type VARCHAR(128),
    error_message TEXT,
    CONSTRAINT ck_mqtt_command_status CHECK (status IN ('ACCEPTED', 'REJECTED', 'EXPIRED', 'RUNNING', 'SUCCEEDED', 'FAILED'))
);
CREATE INDEX idx_mqtt_command_journal_received ON mqtt_command_journal (received_at DESC, command_id);
CREATE INDEX idx_mqtt_command_journal_status ON mqtt_command_journal (status, received_at DESC);
CREATE INDEX idx_mqtt_command_journal_device ON mqtt_command_journal (device_id, received_at DESC);

-- +goose Down
DROP TABLE mqtt_command_journal;
DROP TABLE mqtt_outbox_reservation;
DROP TABLE mqtt_outbox;
DROP TABLE mqtt_config;
