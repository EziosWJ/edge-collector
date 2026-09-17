package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition/script"
)

func TestReliableOutboxPersistsWhileOfflineAndRecoversAfterRestart(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	publisher := &recordingPublisher{}
	connected := false
	worker := NewReliableOutboxWorker(repository, publisher, 4, nil)
	worker.SetConnected(func() bool { return connected })
	payload := `{"schema":"device-event/v1","messageId":"event-restart","edgeId":"edge-01","deviceId":"device-01","timestamp":"2026-09-17T00:00:00Z","data":{}}`
	if err := worker.Submit(OutboxMessage{MessageID: "event-restart", MessageType: OutboxMessageTypeEvent, Topic: "edge/edge-01/device/device-01/event", QoS: 1, Payload: payload, PayloadBytes: int64(len(payload)), Priority: OutboxPriorityReliableEvent}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	waitForOutboxRows(t, repository, 1)
	if got := publicationCount(publisher); got != 0 {
		t.Fatalf("offline publication count = %d", got)
	}
	cancel()
	<-done

	connected = true
	restarted := NewReliableOutboxWorker(repository, publisher, 4, nil)
	restarted.SetConnected(func() bool { return connected })
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- restarted.Run(ctx) }()
	waitForOutboxRows(t, repository, 0)
	cancel()
	<-done
	if got := publicationCount(publisher); got != 1 {
		t.Fatalf("recovered publication count = %d", got)
	}
}

func TestReliableOutboxMarksPublishFailureBeforeRetry(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	publisher := &recordingPublisher{err: errors.New("broker unavailable")}
	worker := NewReliableOutboxWorker(repository, publisher, 4, nil)
	worker.SetConnected(func() bool { return true })
	payload := `{}`
	if err := repository.Enqueue(context.Background(), OutboxMessage{MessageID: "event-failure", MessageType: OutboxMessageTypeEvent, Topic: "event", QoS: 1, Payload: payload, PayloadBytes: 2, Priority: OutboxPriorityReliableEvent}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	waitForOutboxAttempt(t, repository, "event-failure")
	publisher.mu.Lock()
	publisher.err = nil
	publisher.mu.Unlock()
	worker.signal()
	waitForOutboxRows(t, repository, 0)
	cancel()
	<-done
}

func TestReliableOutboxStoresStableDeliveryDiagnostic(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	if err := repository.Enqueue(context.Background(), OutboxMessage{
		MessageID: "event-diagnostic", MessageType: OutboxMessageTypeEvent, Topic: "edge/event",
		QoS: 1, Payload: "{}", PayloadBytes: 2, Priority: OutboxPriorityReliableEvent,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkOutboxAttempt(context.Background(), 1, time.Now().UTC(), errors.New("password=must-not-be-persisted SQL detail")); err != nil {
		t.Fatal(err)
	}
	var message OutboxMessage
	if err := repository.db.Where("message_id=?", "event-diagnostic").Take(&message).Error; err != nil {
		t.Fatal(err)
	}
	if message.LastError == nil || *message.LastError != "MQTT_DELIVERY_FAILED" {
		t.Fatalf("stored delivery diagnostic = %v", message.LastError)
	}
}

func TestEventProjectorUsesGenericReliableEventContract(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	builder, err := NewTopicBuilder("edge", "edge-01")
	if err != nil {
		t.Fatal(err)
	}
	worker := NewReliableOutboxWorker(repository, &recordingPublisher{}, 4, nil)
	projector := NewEventProjector(worker, builder, time.Hour)
	at := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	projector.SetClock(func() time.Time { return at })
	projector.Emit(context.Background(), acquisition.Device{ID: 1, ExternalID: "device-01"}, acquisition.ScriptVersion{ID: 3, ScriptID: 2, VersionNo: 4}, []script.Event{{Kind: "threshold", Key: "temperature", Payload: map[string]any{"value": 12}, At: at}})
	if !worker.persistOne(context.Background()) {
		t.Fatal("event ingress was not persisted")
	}
	message, err := repository.NextOutbox(context.Background(), at)
	if err != nil || message == nil {
		t.Fatalf("event outbox = %#v, error = %v", message, err)
	}
	if message.Topic != "edge/edge-01/device/device-01/event" || message.QoS != 1 || message.Retain != 0 || message.Priority != OutboxPriorityReliableEvent {
		t.Fatalf("event message = %#v", message)
	}
	var envelope Envelope
	if err := json.Unmarshal([]byte(message.Payload), &envelope); err != nil || envelope.Schema != SchemaDeviceEvent {
		t.Fatalf("event payload = %s, error = %v", message.Payload, err)
	}
}

func TestReliableOutboxWorkerRunsRetentionCleanup(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	now := time.Now().UTC()
	expired := now.Add(-time.Hour)
	if err := repository.Enqueue(context.Background(), OutboxMessage{
		MessageID: "expired-event", MessageType: OutboxMessageTypeEvent, Topic: "edge/event",
		QoS: 1, Payload: "{}", PayloadBytes: 2, Priority: OutboxPriorityReliableEvent,
		CreatedAt: expired, ExpiresAt: timePtr(expired),
	}); err != nil {
		t.Fatal(err)
	}
	oldReceived := now.Add(-8 * 24 * time.Hour)
	if err := repository.db.Create(&CommandJournal{
		CommandID: "old-terminal-command", DeviceID: "device-01", CommandName: "close",
		PayloadHash: "old-hash", ReceivedAt: oldReceived, IssuedAt: oldReceived,
		ExpiresAt: oldReceived.Add(time.Hour), Status: CommandStatusSucceeded,
	}).Error; err != nil {
		t.Fatal(err)
	}

	worker := NewReliableOutboxWorker(repository, &recordingPublisher{}, 2, nil)
	worker.SetConnected(func() bool { return false })
	worker.SetCleanupInterval(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats, statsErr := repository.OutboxStats(context.Background())
		var count int64
		countErr := repository.db.Model(&CommandJournal{}).Where("command_id=?", "old-terminal-command").Count(&count).Error
		if statsErr == nil && countErr == nil && stats.Rows == 0 && count == 0 {
			cancel()
			<-done
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatalf("retention cleanup did not remove expired rows")
}

func waitForOutboxRows(t *testing.T, repository *Repository, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats, err := repository.OutboxStats(context.Background())
		if err == nil && stats.Rows == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	stats, _ := repository.OutboxStats(context.Background())
	t.Fatalf("outbox rows = %d, want %d", stats.Rows, want)
}

func waitForOutboxAttempt(t *testing.T, repository *Repository, messageID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var message OutboxMessage
		if err := repository.db.Where("message_id=?", messageID).Take(&message).Error; err == nil && message.AttemptCount > 0 && message.LastError != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("outbox publish failure was not recorded")
}

func publicationCount(publisher *recordingPublisher) int {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	return len(publisher.publications)
}
