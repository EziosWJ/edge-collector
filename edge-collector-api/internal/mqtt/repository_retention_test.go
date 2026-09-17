package mqtt

import (
	"context"
	"testing"
	"time"
)

func TestRepositoryCleanupProtectsUnackedCommandFinal(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()

	now := time.Date(2026, 9, 17, 5, 0, 0, 0, time.UTC)
	commandID := "command-final-retention"
	payload := `{"schema":"device-command-result/v1","messageId":"final-retention","edgeId":"edge-01","deviceId":"device-retention","timestamp":"2026-09-10T05:00:00Z","data":{"commandId":"command-final-retention","name":"close","status":"SUCCEEDED","receivedAt":"2026-09-10T04:59:00Z","startedAt":"2026-09-10T04:59:30Z","completedAt":"2026-09-10T05:00:00Z","result":null,"error":null}}`
	completed := now.Add(-7 * 24 * time.Hour)
	if _, err := repository.AdmitRejectedCommand(context.Background(), CommandJournal{
		CommandID:     commandID,
		DeviceID:      "device-retention",
		CommandName:   "close",
		PayloadHash:   "retention-hash",
		ReceivedAt:    completed,
		IssuedAt:      completed,
		ExpiresAt:     completed.Add(time.Hour),
		Status:        CommandStatusFailed,
		CompletedAt:   &completed,
		ResultPayload: payload,
	}, OutboxMessage{
		MessageID:    "final-retention",
		MessageType:  OutboxMessageTypeCommandResult,
		CommandID:    &commandID,
		Topic:        "edge/edge-01/device/device-retention/command-result",
		QoS:          1,
		Payload:      payload,
		PayloadBytes: int64(len([]byte(payload))),
		Priority:     OutboxPriorityCommandFinal,
		CreatedAt:    completed,
		ExpiresAt:    timePtr(now.Add(-time.Minute)),
	}); err != nil {
		t.Fatal(err)
	}

	if err := repository.Cleanup(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FindCommand(context.Background(), commandID); err != nil {
		t.Fatalf("terminal journal was cleaned before final PUBACK: %v", err)
	}
	next, err := repository.NextOutbox(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.CommandID == nil || *next.CommandID != commandID {
		t.Fatalf("expired command final = %#v, want protected row", next)
	}
}
