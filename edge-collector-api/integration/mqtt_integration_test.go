//go:build integration

package integration

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/mqtt"
	"gorm.io/gorm"
)

func TestSQLiteMQTTPersistenceContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mqtt.db")
	runSQLiteMigrations(t, path)
	database := openSQLiteDatabase(t, path)
	defer func() { _ = database.Close() }()

	runMQTTPersistenceContract(t, database.GORM)
}

func TestPostgresMQTTPersistenceContract(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil || exec.Command("docker", "info").Run() != nil {
		t.Skip("Docker is required for PostgreSQL integration tests")
	}

	database := startPostgres(t)
	runMigrations(t, projectRoot(t), database.dsn)
	connection := openTemporaryDatabase(t, database.dsn)
	defer func() { _ = connection.Close() }()

	runMQTTPersistenceContract(t, connection.GORM)
}

func runMQTTPersistenceContract(t *testing.T, database *gorm.DB) {
	t.Helper()
	box, err := mqtt.NewSecretBox("mqtt-integration-master-secret")
	if err != nil {
		t.Fatal(err)
	}
	repository := mqtt.NewRepository(database, box)
	ctx := context.Background()

	config, err := repository.GetConfig(ctx)
	if err != nil {
		t.Fatalf("load MQTT default config: %v", err)
	}
	input := configInputForIntegration(config)
	input.PasswordAction = mqtt.SecretSet
	input.Password = "mqtt-integration-password"
	input.ClientPrivateKeyAction = mqtt.SecretSet
	input.ClientPrivateKey = "mqtt-integration-private-key"
	input.OutboxMaxRows = 2
	input.OutboxMaxBytes = 1024
	if _, err := repository.SaveConfig(ctx, input, audit.Event{Action: "mqtt.integration.config", Resource: "MQTT 配置"}); err != nil {
		t.Fatalf("save MQTT config: %v", err)
	}

	stored, err := repository.GetConfig(ctx)
	if err != nil {
		t.Fatalf("reload MQTT config: %v", err)
	}
	if stored.PasswordCiphertext == nil || stored.ClientPrivateKeyCiphertext == nil {
		t.Fatalf("encrypted MQTT secrets missing: %#v", stored)
	}
	if strings.Contains(*stored.PasswordCiphertext, input.Password) || strings.Contains(*stored.ClientPrivateKeyCiphertext, input.ClientPrivateKey) {
		t.Fatal("MQTT secret was stored as plaintext")
	}
	view := stored.View()
	if !view.PasswordConfigured || !view.ClientPrivateKeyConfigured {
		t.Fatalf("secret configured flags = %#v", view)
	}
	runtimeConfig, err := repository.RuntimeConfig(ctx)
	if err != nil {
		t.Fatalf("decrypt MQTT runtime config: %v", err)
	}
	if runtimeConfig.Password != input.Password || runtimeConfig.ClientPrivateKey != input.ClientPrivateKey {
		t.Fatal("decrypted MQTT credentials differ from configured values")
	}

	commandID := "mqtt-integration-command"
	acceptedID := commandID
	acceptedPayload := `{"accepted":true}`
	journal := mqtt.CommandJournal{
		CommandID: commandID, DeviceID: "device-integration", CommandName: "close",
		PayloadHash: "integration-command-hash", ReceivedAt: time.Now().UTC(),
		IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Status: mqtt.CommandStatusAccepted,
	}
	accepted := mqtt.OutboxMessage{
		MessageID: "mqtt-integration-accepted", MessageType: mqtt.OutboxMessageTypeCommandAccepted,
		CommandID: &acceptedID, Topic: "edge/edge-01/device/device-integration/command-result", QoS: 1,
		Payload: acceptedPayload, PayloadBytes: int64(len(acceptedPayload)), Priority: mqtt.OutboxPriorityCommandNotice,
	}
	reservation := mqtt.FinalReservation{CommandID: commandID, ReservedRows: 1, ReservedBytes: 512, AcceptedBytes: accepted.PayloadBytes, FinalBytes: 512}
	admission, err := repository.AdmitCommand(ctx, journal, accepted, reservation)
	if err != nil || !admission.Admitted {
		t.Fatalf("admit MQTT command = %#v, error = %v", admission, err)
	}
	duplicate, err := repository.LookupCommand(ctx, commandID, journal.PayloadHash)
	if err != nil || duplicate == nil || duplicate.CommandID != commandID {
		t.Fatalf("lookup admitted MQTT command = %#v, error = %v", duplicate, err)
	}
	if err := repository.Enqueue(ctx, mqtt.OutboxMessage{
		MessageID: "mqtt-integration-event", MessageType: mqtt.OutboxMessageTypeEvent,
		Topic: "edge/edge-01/device/device-integration/event", QoS: 1, Payload: "{}", PayloadBytes: 2,
		Priority: mqtt.OutboxPriorityReliableEvent,
	}); !errors.Is(err, mqtt.ErrReliableResultCapacityExhausted) {
		t.Fatalf("event capacity error = %v, want %v", err, mqtt.ErrReliableResultCapacityExhausted)
	}
	started, err := repository.MarkCommandRunning(ctx, commandID, time.Now().UTC())
	if err != nil || !started {
		t.Fatalf("mark MQTT command running = %t, error = %v", started, err)
	}
	completed := time.Now().UTC()
	finalPayload := `{"schema":"device-command-result/v1","status":"SUCCEEDED"}`
	journal.Status = mqtt.CommandStatusSucceeded
	journal.CompletedAt = &completed
	journal.ResultPayload = finalPayload
	if err := repository.FinalizeCommand(ctx, journal, mqtt.OutboxMessage{
		MessageID: "mqtt-integration-final", MessageType: mqtt.OutboxMessageTypeCommandResult,
		CommandID: &acceptedID, Topic: accepted.Topic, QoS: 1, Payload: finalPayload,
		PayloadBytes: int64(len(finalPayload)), Priority: mqtt.OutboxPriorityCommandFinal,
	}); err != nil {
		t.Fatalf("finalize MQTT command: %v", err)
	}
	stats, err := repository.OutboxStats(ctx)
	if err != nil {
		t.Fatalf("read MQTT outbox stats: %v", err)
	}
	if stats.Rows != 2 {
		t.Fatalf("MQTT outbox rows = %d, want accepted and final", stats.Rows)
	}
	var reservations int64
	if err := database.Model(&mqtt.FinalReservation{}).Count(&reservations).Error; err != nil {
		t.Fatalf("count MQTT reservations: %v", err)
	}
	if reservations != 0 {
		t.Fatalf("MQTT final reservations = %d, want zero after durable final", reservations)
	}
}

func configInputForIntegration(value mqtt.Config) mqtt.ConfigInput {
	return mqtt.ConfigInput{
		Enabled: value.Enabled == 1, EdgeID: value.EdgeID, BrokerURL: value.BrokerURL,
		ProtocolVersion: value.ProtocolVersion, ClientID: value.ClientID, Username: value.Username,
		PasswordAction: mqtt.SecretKeep, TLSEnabled: value.TLSEnabled == 1,
		CACertificate: value.CACertificate, ClientCertificate: value.ClientCertificate,
		ClientPrivateKeyAction: mqtt.SecretKeep, KeepAliveSeconds: value.KeepAliveSeconds,
		ConnectTimeoutMS: value.ConnectTimeoutMS, ReconnectMinMS: value.ReconnectMinMS,
		ReconnectMaxMS: value.ReconnectMaxMS, TopicPrefix: value.TopicPrefix,
		RawPublishIntervalMS: value.RawPublishIntervalMS, OutboxMaxRows: value.OutboxMaxRows,
		OutboxMaxBytes: value.OutboxMaxBytes, OutboxRetentionDays: value.OutboxRetentionDays,
		CommandJournalRetentionDays: value.CommandJournalRetentionDays, CommandJournalMaxRows: value.CommandJournalMaxRows,
		CommandQueueCapacity: value.CommandQueueCapacity, CommandPollFairness: value.CommandPollFairness,
	}
}
