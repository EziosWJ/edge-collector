package mqtt

import (
	"bytes"
	"context"
	"errors"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/config"
	platformdatabase "github.com/EziosWJ/edge-collector/edge-collector-api/internal/platform/database"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestRepositoryEmptyOutboxDoesNotLogRecordNotFound(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()

	var logs bytes.Buffer
	repository.db = repository.db.Session(&gorm.Session{
		Logger: gormlogger.New(log.New(&logs, "", 0), gormlogger.Config{LogLevel: gormlogger.Error}),
	})

	next, err := repository.NextOutbox(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("NextOutbox() error = %v", err)
	}
	if next != nil {
		t.Fatalf("NextOutbox() = %#v, want nil for an empty outbox", next)
	}
	if _, err := repository.OutboxStats(context.Background()); err != nil {
		t.Fatalf("OutboxStats() error = %v", err)
	}
	if strings.Contains(logs.String(), "record not found") {
		t.Fatalf("empty outbox emitted record-not-found log: %s", logs.String())
	}
}

func TestRepositoryEncryptsConfigAndKeepsSecretsOutOfView(t *testing.T) {
	repository, database, cleanup := newSQLiteRepository(t)
	defer cleanup()
	current, err := repository.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	input := configInput(current)
	input.PasswordAction = SecretSet
	input.Password = "broker-password-should-not-leak"
	input.ClientPrivateKeyAction = SecretSet
	input.ClientPrivateKey = "private-key-should-not-leak"
	if _, err := repository.SaveConfig(context.Background(), input, audit.Event{Action: "mqtt.config.update", Resource: "MQTT 配置"}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	view, err := repository.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig() after save error = %v", err)
	}
	if view.PasswordCiphertext == nil || *view.PasswordCiphertext == input.Password {
		t.Fatalf("password was not encrypted: %#v", view.PasswordCiphertext)
	}
	if view.ClientPrivateKeyCiphertext == nil || *view.ClientPrivateKeyCiphertext == input.ClientPrivateKey {
		t.Fatalf("private key was not encrypted: %#v", view.ClientPrivateKeyCiphertext)
	}
	if got := view.View(); got.PasswordConfigured != true || !got.ClientPrivateKeyConfigured {
		t.Fatalf("secret configuration flags = %#v", got)
	}
	serialized := view.View()
	if serialized.Username != current.Username || serialized.PasswordConfigured != true {
		t.Fatalf("unexpected config view = %#v", serialized)
	}
	var raw string
	if err := database.GORM.Table("mqtt_config").Select("password_ciphertext").Where("id=1").Scan(&raw).Error; err != nil {
		t.Fatalf("read ciphertext error = %v", err)
	}
	if raw == input.Password {
		t.Fatal("plaintext password was persisted")
	}
	runtime, err := repository.RuntimeConfig(context.Background())
	if err != nil {
		t.Fatalf("RuntimeConfig() error = %v", err)
	}
	if runtime.Password != input.Password || runtime.ClientPrivateKey != input.ClientPrivateKey {
		t.Fatalf("decrypted runtime credentials do not match")
	}
}

func TestRepositoryAdmissionReservesFinalCapacityAndDeduplicates(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	ctx := context.Background()
	current, err := repository.GetConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := configInput(current)
	input.OutboxMaxRows = 2
	input.OutboxMaxBytes = 1024
	if _, err := repository.SaveConfig(ctx, input, audit.Event{Action: "mqtt.config.capacity", Resource: "MQTT 配置"}); err != nil {
		t.Fatalf("set capacity: %v", err)
	}

	commandID := "command-001"
	acceptedPayload := `{"schema":"device-command-accepted/v1","commandId":"command-001"}`
	acceptedCommandID := commandID
	journal := CommandJournal{CommandID: commandID, DeviceID: "device-01", CommandName: "set_value", PayloadHash: "hash-001", IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Status: CommandStatusAccepted}
	accepted := OutboxMessage{MessageID: "message-accepted-001", MessageType: OutboxMessageTypeCommandAccepted, CommandID: &acceptedCommandID, Topic: "edge/edge-01/device/device-01/command-result", QoS: 1, Payload: acceptedPayload, PayloadBytes: int64(len([]byte(acceptedPayload))), Priority: OutboxPriorityCommandNotice}
	reservation := FinalReservation{CommandID: commandID, ReservedRows: 1, ReservedBytes: 500, AcceptedBytes: accepted.PayloadBytes, FinalBytes: 500}
	result, err := repository.AdmitCommand(ctx, journal, accepted, reservation)
	if err != nil || !result.Admitted || result.Existing != nil {
		t.Fatalf("first admission = %#v, error = %v", result, err)
	}
	if err := repository.Enqueue(ctx, OutboxMessage{MessageID: "event-001", MessageType: OutboxMessageTypeEvent, Topic: "edge/edge-01/device/device-01/event", QoS: 1, Payload: "{}", PayloadBytes: 2, Priority: OutboxPriorityReliableEvent}); !errors.Is(err, ErrReliableResultCapacityExhausted) {
		t.Fatalf("Enqueue() error = %v, want capacity exhaustion", err)
	}

	duplicate, err := repository.AdmitCommand(ctx, journal, accepted, reservation)
	if err != nil || duplicate.Admitted || duplicate.Existing == nil {
		t.Fatalf("duplicate admission = %#v, error = %v", duplicate, err)
	}
	conflictJournal := journal
	conflictJournal.PayloadHash = "hash-002"
	if _, err := repository.AdmitCommand(ctx, conflictJournal, accepted, reservation); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("conflicting command error = %v", err)
	}

	started, err := repository.MarkCommandRunning(ctx, commandID, time.Now().UTC())
	if err != nil || !started {
		t.Fatalf("MarkCommandRunning() = %v, error = %v", started, err)
	}
	finalPayload := `{"schema":"device-command-result/v1","commandId":"command-001","status":"SUCCEEDED"}`
	completed := time.Now().UTC()
	final := OutboxMessage{MessageID: "message-final-001", MessageType: OutboxMessageTypeCommandResult, CommandID: &acceptedCommandID, Topic: "edge/edge-01/device/device-01/command-result", QoS: 1, Payload: finalPayload, PayloadBytes: int64(len([]byte(finalPayload))), Priority: OutboxPriorityCommandFinal}
	journal.Status = CommandStatusSucceeded
	journal.CompletedAt = &completed
	journal.ResultPayload = finalPayload
	if err := repository.FinalizeCommand(ctx, journal, final); err != nil {
		t.Fatalf("FinalizeCommand() error = %v", err)
	}
	stats, err := repository.OutboxStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 2 {
		t.Fatalf("outbox rows = %d, want accepted and final", stats.Rows)
	}
	if err := repository.FinalizeCommand(ctx, journal, final); err != nil {
		t.Fatalf("idempotent finalization error = %v", err)
	}
}

func TestRepositoryCleanupNeverDeletesIncompleteCommandResult(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	ctx := context.Background()
	commandID := "command-cleanup-001"
	acceptedCommandID := commandID
	payload := `{"accepted":true}`
	now := time.Now().UTC()
	_, err := repository.AdmitCommand(ctx,
		CommandJournal{CommandID: commandID, DeviceID: "device-01", CommandName: "test", PayloadHash: "cleanup-hash", ReceivedAt: now.Add(-48 * time.Hour), IssuedAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-24 * time.Hour), Status: CommandStatusAccepted},
		OutboxMessage{MessageID: "cleanup-accepted", MessageType: OutboxMessageTypeCommandAccepted, CommandID: &acceptedCommandID, Topic: "command-result", QoS: 1, Payload: payload, PayloadBytes: int64(len([]byte(payload))), Priority: OutboxPriorityCommandNotice, CreatedAt: now.Add(-48 * time.Hour), ExpiresAt: timePtr(now.Add(-time.Hour))},
		FinalReservation{CommandID: commandID, ReservedRows: 1, ReservedBytes: 100, AcceptedBytes: int64(len([]byte(payload))), FinalBytes: 100, CreatedAt: now.Add(-48 * time.Hour)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Cleanup(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FindCommand(ctx, commandID); err != nil {
		t.Fatalf("incomplete journal cleanup error = %v", err)
	}
	stats, err := repository.OutboxStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 0 {
		// The accepted notice may expire; the incomplete journal is what must
		// remain durable. A final row would be the protected case below.
		t.Fatalf("expired accepted notice rows = %d, want 0", stats.Rows)
	}
}

func TestRepositoryAdmissionDoesNotOversellFinalReservation(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	ctx := context.Background()
	current, err := repository.GetConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := configInput(current)
	input.OutboxMaxRows = 2
	input.OutboxMaxBytes = 1024
	if _, err := repository.SaveConfig(ctx, input, auditEventForTest()); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			commandID := "command-reservation-" + string(rune('a'+index))
			acceptedID := commandID
			acceptedPayload := `{"accepted":true}`
			_, admissionErr := repository.AdmitCommand(ctx,
				CommandJournal{CommandID: commandID, DeviceID: "device-reservation", CommandName: "close", PayloadHash: commandID, IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Status: CommandStatusAccepted},
				OutboxMessage{MessageID: "accepted-" + commandID, MessageType: OutboxMessageTypeCommandAccepted, CommandID: &acceptedID, Topic: "command-result", QoS: 1, Payload: acceptedPayload, PayloadBytes: int64(len(acceptedPayload)), Priority: OutboxPriorityCommandNotice},
				FinalReservation{CommandID: commandID, ReservedRows: 1, ReservedBytes: 500, AcceptedBytes: int64(len(acceptedPayload)), FinalBytes: 500},
			)
			results <- admissionErr
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	admitted := 0
	for admissionErr := range results {
		if admissionErr == nil {
			admitted++
			continue
		}
		if !errors.Is(admissionErr, ErrReliableResultCapacityExhausted) {
			t.Fatalf("concurrent admission error = %v, want capacity exhaustion for the loser", admissionErr)
		}
	}
	if admitted != 1 {
		t.Fatalf("concurrent admissions = %d, want exactly one", admitted)
	}
	var reservations int64
	if err := repository.db.Model(&FinalReservation{}).Count(&reservations).Error; err != nil {
		t.Fatal(err)
	}
	if reservations != 1 {
		t.Fatalf("final reservations = %d, want one", reservations)
	}
	var rows int64
	if err := repository.db.Model(&OutboxMessage{}).Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("accepted outbox rows = %d, want one", rows)
	}
}

func newSQLiteRepository(t *testing.T) (*Repository, *platformdatabase.Database, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mqtt.db")
	database, err := platformdatabase.Open(context.Background(), structDatabaseConfig(path))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	dialect := "sqlite3"
	goose.SetDialect(dialect)
	goose.SetTableName("goose_schema_db_version")
	if err := goose.UpContext(context.Background(), database.SQL, filepath.Join("..", "..", "migrations", "sqlite", "schema")); err != nil {
		database.Close()
		t.Fatalf("apply schema migrations: %v", err)
	}
	goose.SetTableName("goose_seed_db_version")
	if err := goose.UpContext(context.Background(), database.SQL, filepath.Join("..", "..", "migrations", "sqlite", "seed")); err != nil {
		database.Close()
		t.Fatalf("apply seed migrations: %v", err)
	}
	box, err := NewSecretBox("test-master-secret")
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	repository := NewRepository(database.GORM, box)
	return repository, database, func() { _ = database.Close() }
}

func structDatabaseConfig(path string) config.DatabaseConfig {
	return config.DatabaseConfig{Driver: platformdatabase.DriverSQLite, URL: path}
}

func configInput(value Config) ConfigInput {
	return ConfigInput{Enabled: value.Enabled == 1, EdgeID: value.EdgeID, BrokerURL: value.BrokerURL, ProtocolVersion: value.ProtocolVersion, ClientID: value.ClientID, Username: value.Username, PasswordAction: SecretKeep, TLSEnabled: value.TLSEnabled == 1, CACertificate: value.CACertificate, ClientCertificate: value.ClientCertificate, ClientPrivateKeyAction: SecretKeep, KeepAliveSeconds: value.KeepAliveSeconds, ConnectTimeoutMS: value.ConnectTimeoutMS, ReconnectMinMS: value.ReconnectMinMS, ReconnectMaxMS: value.ReconnectMaxMS, TopicPrefix: value.TopicPrefix, RawPublishIntervalMS: value.RawPublishIntervalMS, OutboxMaxRows: value.OutboxMaxRows, OutboxMaxBytes: value.OutboxMaxBytes, OutboxRetentionDays: value.OutboxRetentionDays, CommandJournalRetentionDays: value.CommandJournalRetentionDays, CommandJournalMaxRows: value.CommandJournalMaxRows, CommandQueueCapacity: value.CommandQueueCapacity, CommandPollFairness: value.CommandPollFairness}
}

func timePtr(value time.Time) *time.Time { return &value }
