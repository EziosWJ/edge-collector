-- +goose Up
INSERT INTO mqtt_config (
    id, enabled, edge_id, broker_url, protocol_version, client_id, username,
    tls_enabled, ca_certificate, client_certificate, keep_alive_seconds,
    connect_timeout_ms, reconnect_min_ms, reconnect_max_ms, topic_prefix,
    raw_publish_interval_ms, outbox_max_rows, outbox_max_bytes,
    outbox_retention_days, command_journal_retention_days,
    command_journal_max_rows, command_queue_capacity, command_poll_fairness
) VALUES (
    1, 0, 'edge-01', 'mqtt://127.0.0.1:1883', 'MQTT_5', 'edge-collector', '',
    0, '', '', 30, 10000, 1000, 60000, 'edge', 1000, 10000, 67108864,
    7, 7, 10000, 32, 1
);

-- +goose Down
DELETE FROM mqtt_config WHERE id = 1;
