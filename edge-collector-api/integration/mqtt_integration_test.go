//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/config"
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
	dsn := postgresMQTTDSN(t)
	runMigrations(t, projectRoot(t), dsn)
	connection := openTemporaryDatabase(t, dsn)
	defer func() { _ = connection.Close() }()

	runMQTTPersistenceContract(t, connection.GORM)
}

func TestPostgresMQTTRuntimeTopicPayloadContract(t *testing.T) {
	dsn := postgresMQTTDSN(t)
	runMigrations(t, projectRoot(t), dsn)
	connection := openTemporaryDatabase(t, dsn)
	defer func() { _ = connection.Close() }()

	repository := mqtt.NewRepository(connection.GORM)
	current, err := repository.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("load MQTT default config: %v", err)
	}
	input := configInputForIntegration(current)
	input.Enabled = true
	input.EdgeID = "edge-pg-integration"
	input.TopicPrefix = "edge/telemetry"
	input.ClientID = "postgres-runtime-client"
	if _, err := repository.SaveConfig(context.Background(), input, audit.Event{Action: "mqtt.integration.runtime", Resource: "MQTT 配置"}); err != nil {
		t.Fatalf("save PostgreSQL MQTT runtime config: %v", err)
	}

	transport := &postgresMQTTTransport{}
	factory := func(_ context.Context, runtimeConfig mqtt.RuntimeConfig, callbacks mqtt.TransportCallbacks) (mqtt.Transport, error) {
		transport.mu.Lock()
		transport.runtimeConfig = runtimeConfig
		transport.callbacks = callbacks
		transport.mu.Unlock()
		return transport, nil
	}
	runtime := mqtt.NewRuntime(repository, factory)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	waitForMQTTIntegration(t, func() bool {
		transport.mu.Lock()
		defer transport.mu.Unlock()
		return transport.callbacks.OnConnected != nil
	})
	transport.mu.Lock()
	onConnected := transport.callbacks.OnConnected
	transport.mu.Unlock()
	onConnected()
	waitForMQTTIntegration(t, func() bool { return runtime.State().State == mqtt.RuntimeStateConnected })

	builder, err := mqtt.NewTopicBuilder(input.TopicPrefix, input.EdgeID)
	if err != nil {
		t.Fatalf("build PostgreSQL MQTT topic builder: %v", err)
	}
	state := runtime.State()
	if state.SubscriptionFilter != builder.CommandSubscription() {
		t.Fatalf("PostgreSQL MQTT subscription filter = %q, want %q", state.SubscriptionFilter, builder.CommandSubscription())
	}
	transport.mu.Lock()
	runtimeConfig := transport.runtimeConfig
	subscriptions := append([]string(nil), transport.subscriptions...)
	transport.mu.Unlock()
	if runtimeConfig.EdgeID != input.EdgeID || runtimeConfig.TopicPrefix != input.TopicPrefix || runtimeConfig.ClientID != input.ClientID {
		t.Fatalf("PostgreSQL MQTT runtime config = %#v", runtimeConfig.Config)
	}
	if len(subscriptions) != 1 || subscriptions[0] != builder.CommandSubscription() {
		t.Fatalf("PostgreSQL MQTT subscriptions = %v, want %q", subscriptions, builder.CommandSubscription())
	}

	at := time.Date(2026, 9, 18, 1, 2, 3, 456000000, time.FixedZone("CST", 8*60*60))
	payload, err := mqtt.BuildEdgeStatus(input.EdgeID, "postgres-runtime-message", at, true, "connected")
	if err != nil {
		t.Fatalf("build PostgreSQL MQTT status payload: %v", err)
	}
	if err := runtime.Publish(ctx, mqtt.Publication{Topic: builder.EdgeStatus(), QoS: 1, Retain: true, Payload: payload}); err != nil {
		t.Fatalf("publish PostgreSQL MQTT status payload: %v", err)
	}
	transport.mu.Lock()
	publications := append([]mqtt.Publication(nil), transport.publications...)
	transport.mu.Unlock()
	if len(publications) != 1 || publications[0].Topic != builder.EdgeStatus() || publications[0].QoS != 1 || !publications[0].Retain {
		t.Fatalf("PostgreSQL MQTT publication = %#v", publications)
	}
	var envelope mqtt.Envelope
	if err := json.Unmarshal(publications[0].Payload, &envelope); err != nil {
		t.Fatalf("decode PostgreSQL MQTT status payload: %v", err)
	}
	if envelope.Schema != mqtt.SchemaEdgeStatus || envelope.EdgeID != input.EdgeID || envelope.Timestamp != at.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("PostgreSQL MQTT status envelope = %#v", envelope)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PostgreSQL MQTT runtime did not stop")
	}
}

type postgresMQTTTransport struct {
	mu            sync.Mutex
	runtimeConfig mqtt.RuntimeConfig
	callbacks     mqtt.TransportCallbacks
	subscriptions []string
	publications  []mqtt.Publication
}

func (t *postgresMQTTTransport) Publish(_ context.Context, publication mqtt.Publication) error {
	t.mu.Lock()
	t.publications = append(t.publications, mqtt.Publication{Topic: publication.Topic, QoS: publication.QoS, Retain: publication.Retain, Payload: append([]byte(nil), publication.Payload...)})
	t.mu.Unlock()
	return nil
}

func (t *postgresMQTTTransport) Subscribe(_ context.Context, topic string, _ byte) error {
	t.mu.Lock()
	t.subscriptions = append(t.subscriptions, topic)
	t.mu.Unlock()
	return nil
}

func (t *postgresMQTTTransport) Close() error { return nil }

func waitForMQTTIntegration(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("MQTT integration condition was not reached")
}

func postgresMQTTDSN(t *testing.T) string {
	t.Helper()
	root := projectRoot(t)
	configDir := filepath.Join(root, "configs")
	if _, err := os.Stat(filepath.Join(configDir, "config.dev.yaml")); err == nil {
		t.Setenv("APP_ENV", config.EnvironmentDev)
		t.Setenv("APP_CONFIG_PROFILE", "")
		loaded, err := config.LoadFromDir(configDir)
		if err != nil {
			t.Fatalf("load local PostgreSQL config: %v", err)
		}
		if loaded.Database.Driver != "postgres" {
			t.Fatalf("local integration database driver = %q, want postgres", loaded.Database.Driver)
		}
		return startConfiguredPostgresSchema(t, loaded.Database)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspect local PostgreSQL config: %v", err)
	}

	if _, err := exec.LookPath("docker"); err != nil || exec.Command("docker", "info").Run() != nil {
		t.Skip("local PostgreSQL config is unavailable and Docker is required for PostgreSQL integration tests")
	}
	return startPostgres(t).dsn
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
		PayloadHash: strings.Repeat("a", 64), ReceivedAt: time.Now().UTC(),
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
