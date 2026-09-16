//go:build integration

package integration

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
	"github.com/pressly/goose/v3"
)

func TestSQLiteAcquisitionTransportMigrationPreservesLegacyConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	database := openSQLiteDatabase(t, path)
	defer func() { _ = database.Close() }()

	ctx := context.Background()
	goose.SetDialect("sqlite3")
	goose.SetTableName("goose_schema_db_version")
	if err := goose.UpToContext(ctx, database.SQL, filepath.Join(projectRoot(t), "migrations", "sqlite", "schema"), 10); err != nil {
		t.Fatalf("apply legacy schema migrations: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, `
		INSERT INTO acquisition_channel (id, name, port, baud_rate, data_bits, stop_bits, parity, timeout_ms, enabled, inter_request_delay_ms)
		VALUES (41, 'legacy RTU', '/dev/ttyUSB41', 9600, 8, 1, 'E', 450, 1, 25)`); err != nil {
		t.Fatalf("insert legacy channel: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, `
		INSERT INTO acquisition_device (id, name, device_type, channel_id, slave_id, poll_interval_ms, failure_threshold, enabled)
		VALUES (42, 'legacy device', 'FEED_PROTECTOR', 41, 7, 1200, 4, 1)`); err != nil {
		t.Fatalf("insert legacy device: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, `
		INSERT INTO acquisition_register_block (id, device_id, name, function_code, start_address, quantity, sort_order)
		VALUES (43, 42, 'legacy block', 3, 10, 2, 0)`); err != nil {
		t.Fatalf("insert legacy register block: %v", err)
	}

	if err := goose.UpContext(ctx, database.SQL, filepath.Join(projectRoot(t), "migrations", "sqlite", "schema")); err != nil {
		t.Fatalf("apply transport migration: %v", err)
	}

	var protocol, port, parity string
	var baudRate, dataBits, stopBits, timeout, delay, unitID, blockCount int
	if err := database.SQL.QueryRowContext(ctx, `SELECT protocol, timeout_ms, inter_request_delay_ms FROM acquisition_channel WHERE id = 41`).Scan(&protocol, &timeout, &delay); err != nil {
		t.Fatalf("read migrated channel: %v", err)
	}
	if err := database.SQL.QueryRowContext(ctx, `SELECT port, baud_rate, data_bits, stop_bits, parity FROM acquisition_serial_channel WHERE channel_id = 41`).Scan(&port, &baudRate, &dataBits, &stopBits, &parity); err != nil {
		t.Fatalf("read migrated serial channel: %v", err)
	}
	if err := database.SQL.QueryRowContext(ctx, `SELECT unit_id FROM acquisition_device WHERE id = 42`).Scan(&unitID); err != nil {
		t.Fatalf("read migrated device: %v", err)
	}
	if err := database.SQL.QueryRowContext(ctx, `SELECT count(*) FROM acquisition_register_block WHERE device_id = 42`).Scan(&blockCount); err != nil {
		t.Fatalf("read migrated register blocks: %v", err)
	}
	if protocol != "MODBUS_RTU" || port != "/dev/ttyUSB41" || baudRate != 9600 || dataBits != 8 || stopBits != 1 || parity != "E" || timeout != 450 || delay != 25 || unitID != 7 || blockCount != 1 {
		t.Fatalf("migrated configuration = protocol %q port %q baud %d data %d stop %d parity %q timeout %d delay %d unit %d blocks %d", protocol, port, baudRate, dataBits, stopBits, parity, timeout, delay, unitID, blockCount)
	}

	channels, devices, err := acquisition.NewRepository(database.GORM).EnabledConfiguration(ctx)
	if err != nil {
		t.Fatalf("load migrated runtime configuration: %v", err)
	}
	if len(channels) != 1 || channels[0].SerialConfig == nil || channels[0].SerialConfig.Port != "/dev/ttyUSB41" {
		t.Fatalf("loaded migrated channel = %#v, want serial configuration", channels)
	}
	if len(devices) != 1 || devices[0].UnitID != 7 || len(devices[0].RegisterBlocks) != 1 {
		t.Fatalf("loaded migrated device = %#v, want unit ID and register block", devices)
	}

	if err := goose.DownToContext(ctx, database.SQL, filepath.Join(projectRoot(t), "migrations", "sqlite", "schema"), 10); err != nil {
		t.Fatalf("roll back transport migration: %v", err)
	}
	var legacyPort string
	var legacySlaveID, legacyBlockCount int
	if err := database.SQL.QueryRowContext(ctx, `SELECT port FROM acquisition_channel WHERE id = 41`).Scan(&legacyPort); err != nil {
		t.Fatalf("read rolled-back channel: %v", err)
	}
	if err := database.SQL.QueryRowContext(ctx, `SELECT slave_id FROM acquisition_device WHERE id = 42`).Scan(&legacySlaveID); err != nil {
		t.Fatalf("read rolled-back device: %v", err)
	}
	if err := database.SQL.QueryRowContext(ctx, `SELECT count(*) FROM acquisition_register_block WHERE device_id = 42`).Scan(&legacyBlockCount); err != nil {
		t.Fatalf("read rolled-back register blocks: %v", err)
	}
	if legacyPort != "/dev/ttyUSB41" || legacySlaveID != 7 || legacyBlockCount != 1 {
		t.Fatalf("rolled-back configuration = port %q slave %d blocks %d", legacyPort, legacySlaveID, legacyBlockCount)
	}
}

func TestSQLiteAcquisitionTransportMigrationRejectsUnsafeRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network.db")
	database := openSQLiteDatabase(t, path)
	defer func() { _ = database.Close() }()

	ctx := context.Background()
	goose.SetDialect("sqlite3")
	goose.SetTableName("goose_schema_db_version")
	migrations := filepath.Join(projectRoot(t), "migrations", "sqlite", "schema")
	if err := goose.UpToContext(ctx, database.SQL, migrations, 10); err != nil {
		t.Fatalf("apply legacy schema migrations: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, `
		INSERT INTO acquisition_channel (id, name, port, baud_rate, data_bits, stop_bits, parity, timeout_ms, enabled, inter_request_delay_ms)
		VALUES (51, 'network channel', '/dev/ttyUSB51', 9600, 8, 1, 'N', 450, 1, 0)`); err != nil {
		t.Fatalf("insert legacy channel: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, `
		INSERT INTO acquisition_device (id, name, device_type, channel_id, slave_id, poll_interval_ms, failure_threshold, enabled)
		VALUES (52, 'network device', 'FEED_PROTECTOR', 51, 1, 1000, 3, 1)`); err != nil {
		t.Fatalf("insert legacy device: %v", err)
	}
	if err := goose.UpContext(ctx, database.SQL, migrations); err != nil {
		t.Fatalf("apply transport migration: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, `
		UPDATE acquisition_channel SET protocol = 'MODBUS_TCP' WHERE id = 51`); err != nil {
		t.Fatalf("configure network channel: %v", err)
	}
	if _, err := database.SQL.ExecContext(ctx, `
		INSERT INTO acquisition_network_device (device_id, host, port) VALUES (52, '192.0.2.52', 502)`); err != nil {
		t.Fatalf("configure network endpoint: %v", err)
	}

	if err := goose.DownToContext(ctx, database.SQL, migrations, 10); err == nil {
		t.Fatal("unsafe transport rollback unexpectedly succeeded")
	}
	var protocol, host string
	if err := database.SQL.QueryRowContext(ctx, `SELECT protocol FROM acquisition_channel WHERE id = 51`).Scan(&protocol); err != nil {
		t.Fatalf("read guarded channel: %v", err)
	}
	if err := database.SQL.QueryRowContext(ctx, `SELECT host FROM acquisition_network_device WHERE device_id = 52`).Scan(&host); err != nil {
		t.Fatalf("read guarded endpoint: %v", err)
	}
	if protocol != "MODBUS_TCP" || host != "192.0.2.52" {
		t.Fatalf("guarded configuration = protocol %q host %q, want transport data preserved", protocol, host)
	}
}
