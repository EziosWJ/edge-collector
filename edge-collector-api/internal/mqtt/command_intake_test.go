package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition/script"
	"gorm.io/gorm"
)

func TestCommandIntakeAdmitsOnceAndFinalizesWithoutDuplicateExecution(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	builder, err := NewTopicBuilder("edge", "edge-01")
	if err != nil {
		t.Fatal(err)
	}
	device := acquisition.Device{ID: 41, ExternalID: "device-41", Enabled: acquisition.Enabled, ScriptID: int64Pointer(7)}
	resolver := &intakeResolverFake{device: device}
	runtime := &intakeRuntimeFake{version: acquisition.ScriptVersion{ID: 8, ScriptID: 7, VersionNo: 2}}
	intake := NewCommandIntake(repository, resolver, runtime, builder)
	at := time.Date(2026, 9, 17, 1, 0, 0, 0, time.UTC)
	intake.SetClock(func() time.Time { return at })
	payload := commandPayloadForTest(t, "command-41", "device-41", "set_value", map[string]any{"value": 7}, at.Add(-time.Minute), at.Add(time.Hour))
	topic := "edge/edge-01/device/device-41/command"
	if err := intake.Handle(context.Background(), topic, payload); err != nil {
		t.Fatalf("first Handle() error = %v", err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("enqueue requests = %d, want one", len(runtime.requests))
	}
	if err := intake.Handle(context.Background(), topic, payload); err != nil {
		t.Fatalf("duplicate Handle() error = %v", err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("duplicate enqueue requests = %d", len(runtime.requests))
	}
	conflict := commandPayloadForTest(t, "command-41", "device-41", "open", map[string]any{}, at.Add(-time.Minute), at.Add(time.Hour))
	if err := intake.Handle(context.Background(), topic, conflict); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("conflict error = %v", err)
	}

	request := runtime.requests[0]
	if err := request.OnStarted(); err != nil {
		t.Fatalf("OnStarted() error = %v", err)
	}
	runtime.complete(acquisition.CommandExecutionResult{Version: scriptVersionForTest(runtime.version), Result: script.Result{Output: map[string]any{"accepted": true}}})
	waitForCommandStatus(t, repository, "command-41", CommandStatusSucceeded)
	journal, err := repository.FindCommand(context.Background(), "command-41")
	if err != nil || journal == nil {
		t.Fatalf("journal lookup = %#v, error = %v", journal, err)
	}
	var journalRecord CommandJournal
	if err := repository.db.Where("command_id=?", "command-41").Take(&journalRecord).Error; err != nil {
		t.Fatal(err)
	}
	if journalRecord.ResultPayload == "" || journal.StartedAt == nil || journal.CompletedAt == nil {
		t.Fatalf("final journal = %#v", journalRecord)
	}

	var rows []OutboxMessage
	if err := repository.db.Where("command_id=?", "command-41").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if err := repository.DeleteOutbox(context.Background(), row.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := intake.Handle(context.Background(), topic, payload); err != nil {
		t.Fatalf("terminal duplicate Handle() error = %v", err)
	}
	stats, err := repository.OutboxStats(context.Background())
	if err != nil || stats.Rows != 1 {
		t.Fatalf("requeued final stats = %#v, error = %v", stats, err)
	}
}

func TestCommandIntakeRejectsExpiredAndDoesNotEnqueue(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	builder, _ := NewTopicBuilder("edge", "edge-01")
	resolver := &intakeResolverFake{device: acquisition.Device{ID: 42, ExternalID: "device-42", Enabled: acquisition.Enabled, ScriptID: int64Pointer(7)}}
	runtime := &intakeRuntimeFake{}
	intake := NewCommandIntake(repository, resolver, runtime, builder)
	at := time.Date(2026, 9, 17, 2, 0, 0, 0, time.UTC)
	intake.SetClock(func() time.Time { return at })
	payload := commandPayloadForTest(t, "command-expired", "device-42", "close", map[string]any{}, at.Add(-2*time.Hour), at.Add(-time.Minute))
	if err := intake.Handle(context.Background(), "edge/edge-01/device/device-42/command", payload); err != nil {
		t.Fatalf("expired Handle() error = %v", err)
	}
	if len(runtime.requests) != 0 {
		t.Fatalf("expired enqueue requests = %d", len(runtime.requests))
	}
	view, err := repository.FindCommand(context.Background(), "command-expired")
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != CommandStatusExpired {
		t.Fatalf("expired status = %s", view.Status)
	}
	var result OutboxMessage
	if err := repository.db.Where("command_id=?", "command-expired").Take(&result).Error; err != nil {
		t.Fatal(err)
	}
	if result.Priority != OutboxPriorityCommandFinal || result.QoS != 1 {
		t.Fatalf("expired result row = %#v", result)
	}
}

func TestCommandIntakeCapacityAdmissionPreventsExecution(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	current, err := repository.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	input := configInput(current)
	input.OutboxMaxRows = 1
	if _, err := repository.SaveConfig(context.Background(), input, auditEventForTest()); err != nil {
		t.Fatal(err)
	}
	builder, _ := NewTopicBuilder("edge", "edge-01")
	resolver := &intakeResolverFake{device: acquisition.Device{ID: 43, ExternalID: "device-43", Enabled: acquisition.Enabled, ScriptID: int64Pointer(7)}}
	runtime := &intakeRuntimeFake{}
	intake := NewCommandIntake(repository, resolver, runtime, builder)
	at := time.Date(2026, 9, 17, 3, 0, 0, 0, time.UTC)
	intake.SetClock(func() time.Time { return at })
	payload := commandPayloadForTest(t, "command-capacity", "device-43", "close", map[string]any{}, at.Add(-time.Minute), at.Add(time.Hour))
	err = intake.Handle(context.Background(), "edge/edge-01/device/device-43/command", payload)
	if !errors.Is(err, ErrReliableResultCapacityExhausted) {
		t.Fatalf("capacity error = %v", err)
	}
	if len(runtime.requests) != 0 {
		t.Fatalf("capacity enqueue requests = %d", len(runtime.requests))
	}
	if _, err := repository.FindCommand(context.Background(), "command-capacity"); !errors.Is(err, gorm.ErrRecordNotFound) {
		// The helper below keeps this assertion independent of GORM's concrete
		// error value while still requiring that no journal was admitted.
		var count int64
		if countErr := repository.db.Model(&CommandJournal{}).Where("command_id=?", "command-capacity").Count(&count).Error; countErr != nil || count != 0 {
			t.Fatalf("capacity journal rows = %d, lookup error = %v", count, err)
		}
	}
}

func TestCommandIntakeRecoversAcceptedCommandFromStoredPayload(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	builder, _ := NewTopicBuilder("edge", "edge-01")
	device := acquisition.Device{ID: 44, ExternalID: "device-44", Enabled: acquisition.Enabled, ScriptID: int64Pointer(7)}
	runtime := &intakeRuntimeFake{version: acquisition.ScriptVersion{ID: 9, ScriptID: 7, VersionNo: 3}}
	resolver := &intakeResolverFake{device: device}
	intake := NewCommandIntake(repository, resolver, runtime, builder)
	at := time.Date(2026, 9, 17, 4, 0, 0, 0, time.UTC)
	intake.SetClock(func() time.Time { return at })
	payload := commandPayloadForTest(t, "command-recover", "device-44", "set_value", map[string]any{"value": 8}, at.Add(-time.Minute), at.Add(time.Hour))
	command, err := DecodeCommand(payload, DefaultMaxCommandBytes, at)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := CanonicalCommandHash(command)
	if err != nil {
		t.Fatal(err)
	}
	commandID := command.CommandID
	now := at
	acceptedPayload := `{"schema":"device-command-result/v1","messageId":"accepted-recover","edgeId":"edge-01","deviceId":"device-44","timestamp":"2026-09-17T04:00:00Z","data":{"commandId":"command-recover","name":"set_value","status":"ACCEPTED","receivedAt":"2026-09-17T04:00:00Z","startedAt":null,"completedAt":null,"result":null,"error":null}}`
	_, err = repository.AdmitCommand(context.Background(), CommandJournal{CommandID: commandID, DeviceID: command.DeviceID, CommandName: command.Name, PayloadHash: hash, CommandPayload: string(payload), ReceivedAt: now, IssuedAt: command.IssuedAt, ExpiresAt: command.ExpiresAt, Status: CommandStatusAccepted}, OutboxMessage{MessageID: "accepted-recover", MessageType: OutboxMessageTypeCommandAccepted, CommandID: &commandID, Topic: "edge/edge-01/device/device-44/command-result", QoS: 1, Payload: acceptedPayload, PayloadBytes: int64(len(acceptedPayload)), Priority: OutboxPriorityCommandNotice}, FinalReservation{CommandID: commandID, ReservedRows: 1, ReservedBytes: DefaultResultReservationBytes, AcceptedBytes: int64(len(acceptedPayload)), FinalBytes: DefaultResultReservationBytes})
	if err != nil {
		t.Fatal(err)
	}
	if err := intake.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("recovery enqueue requests = %d", len(runtime.requests))
	}
	if err := runtime.requests[0].OnStarted(); err != nil {
		t.Fatal(err)
	}
	runtime.complete(acquisition.CommandExecutionResult{Version: scriptVersionForTest(runtime.version), Result: script.Result{Output: map[string]any{"recovered": true}}})
	waitForCommandStatus(t, repository, commandID, CommandStatusSucceeded)
}

type intakeResolverFake struct {
	device acquisition.Device
	err    error
}

func (f *intakeResolverFake) FindDeviceByExternalID(context.Context, string) (*acquisition.Device, error) {
	if f.err != nil {
		return nil, f.err
	}
	device := f.device
	return &device, nil
}

type intakeRuntimeFake struct {
	mu            sync.Mutex
	version       acquisition.ScriptVersion
	validationErr error
	enqueueErr    error
	requests      []acquisition.CommandRequest
	completions   []func(acquisition.CommandExecutionResult)
}

func (f *intakeRuntimeFake) ValidateCommandTarget(context.Context, int64, string) (acquisition.Device, acquisition.ScriptVersion, error) {
	if f.validationErr != nil {
		return acquisition.Device{}, acquisition.ScriptVersion{}, f.validationErr
	}
	return acquisition.Device{ID: 1, Enabled: acquisition.Enabled}, f.version, nil
}

func (f *intakeRuntimeFake) EnqueueCommand(_ context.Context, request acquisition.CommandRequest) (*acquisition.CommandFuture, error) {
	if f.enqueueErr != nil {
		return nil, f.enqueueErr
	}
	f.mu.Lock()
	f.requests = append(f.requests, request)
	f.mu.Unlock()
	future, complete := acquisition.NewCommandFuture()
	f.mu.Lock()
	f.completions = append(f.completions, complete)
	f.mu.Unlock()
	return future, nil
}

func (f *intakeRuntimeFake) complete(result acquisition.CommandExecutionResult) {
	f.mu.Lock()
	completions := append([]func(acquisition.CommandExecutionResult){}, f.completions...)
	f.mu.Unlock()
	for _, complete := range completions {
		complete(result)
	}
}

func commandPayloadForTest(t *testing.T, commandID, deviceID, name string, args map[string]any, issuedAt, expiresAt time.Time) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"schema": SchemaDeviceCommand, "commandId": commandID, "deviceId": deviceID, "name": name, "args": args, "issuedAt": issuedAt.Format(time.RFC3339Nano), "expiresAt": expiresAt.Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func waitForCommandStatus(t *testing.T, repository *Repository, commandID, status string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		view, err := repository.FindCommand(context.Background(), commandID)
		if err == nil && view.Status == status {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	view, err := repository.FindCommand(context.Background(), commandID)
	t.Fatalf("command %s status = %#v, error = %v, want %s", commandID, view, err, status)
}

func int64Pointer(value int64) *int64 { return &value }

func scriptVersionForTest(value acquisition.ScriptVersion) script.ScriptVersion {
	return script.ScriptVersion{ScriptID: value.ScriptID, VersionID: value.ID, VersionNo: value.VersionNo, Source: value.Source, Checksum: value.Checksum}
}
