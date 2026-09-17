package mqtt

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
)

func TestTopicBuilderUsesStableIdentitiesAndStrictCommandFilter(t *testing.T) {
	builder, err := NewTopicBuilder("edge/telemetry", "edge-01")
	if err != nil {
		t.Fatal(err)
	}
	if got := builder.EdgeStatus(); got != "edge/telemetry/edge-01/status" {
		t.Fatalf("EdgeStatus() = %q", got)
	}
	if got := builder.CommandSubscription(); got != "edge/telemetry/edge-01/device/+/command" {
		t.Fatalf("CommandSubscription() = %q", got)
	}
	if got, err := builder.ParseCommandTopic("edge/telemetry/edge-01/device/device-01/command"); err != nil || got != "device-01" {
		t.Fatalf("ParseCommandTopic() = %q, %v", got, err)
	}
	for _, value := range []string{"edge/+/telemetry", "edge//telemetry", "edge/telemetry/edge-01/device/#/command"} {
		if _, err := NewTopicBuilder(value, "edge-01"); err == nil {
			t.Fatalf("NewTopicBuilder(%q) accepted invalid identity", value)
		}
	}
}

func TestStatusRawAndLWTPayloadsKeepTimestampSemantics(t *testing.T) {
	at := time.Date(2026, 9, 17, 7, 0, 0, 123456789, time.FixedZone("CST", 8*60*60))
	will, err := BuildEdgeStatus("edge-01", "will-1", at, false, "last_will")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(will), "offlineAt") || !strings.Contains(string(will), `"reason":"last_will"`) || !strings.Contains(string(will), `"timestamp":"2026-09-16T23:00:00.123456789Z"`) {
		t.Fatalf("LWT payload = %s", will)
	}
	first := uint16(14)
	state := acquisition.CurrentState{DeviceID: 1, Status: acquisition.StatusOnline, RegisterBlocks: []acquisition.RegisterBlockState{
		{ID: 2, Name: "b", FunctionCode: 3, StartAddress: 200, Quantity: 2, SortOrder: 2, Values: []*uint16{nil, &first}, Valid: false},
		{ID: 1, Name: "a", FunctionCode: 3, StartAddress: 100, Quantity: 1, SortOrder: 1, Values: []*uint16{&first}, Valid: true},
	}}
	raw, err := BuildRawSnapshot("edge-01", "device-01", "raw-1", state, at)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"value":null`) || strings.Index(string(raw), `"name":"a"`) > strings.Index(string(raw), `"name":"b"`) {
		t.Fatalf("raw payload does not preserve null/order: %s", raw)
	}
	var decoded Envelope
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Schema != SchemaRawSnapshot || decoded.DeviceID != "device-01" {
		t.Fatalf("decoded raw envelope = %#v, %v", decoded, err)
	}
	firstRead, err := BuildRawSnapshot("edge-01", "device-01", "raw-2", acquisition.CurrentState{Status: acquisition.StatusInitial, RegisterBlocks: []acquisition.RegisterBlockState{{Name: "unread", FunctionCode: 3, StartAddress: 300, Quantity: 2, Values: nil}}}, at)
	if err != nil || strings.Count(string(firstRead), `"value":null`) != 2 {
		t.Fatalf("first-read null registers = %s, error = %v", firstRead, err)
	}
}

func TestCommandCodecStrictExpiryAndCanonicalHash(t *testing.T) {
	payload := []byte(`{"schema":"device-command/v1","commandId":"cmd-1","deviceId":"device-01","name":"close","args":{"b":2,"a":1},"issuedAt":"2026-09-17T00:00:00+08:00","expiresAt":"2026-09-17T01:00:00+08:00"}`)
	command, err := DecodeCommand(payload, 1024, time.Date(2026, 9, 17, 0, 30, 0, 0, time.FixedZone("CST", 8*60*60)))
	if err != nil {
		t.Fatal(err)
	}
	reordered := command
	reordered.Args = map[string]any{"a": json.Number("1"), "b": json.Number("2")}
	firstHash, err := CanonicalCommandHash(command)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := CanonicalCommandHash(reordered)
	if err != nil || firstHash != secondHash {
		t.Fatalf("canonical hashes = %q/%q, error = %v", firstHash, secondHash, err)
	}
	if _, err := DecodeCommand([]byte(`{"schema":"device-command/v1","commandId":"cmd-1","deviceId":"device-01","name":"close","args":{},"issuedAt":"2026-09-17T00:00:00Z","expiresAt":"2026-09-17T01:00:00Z","unexpected":true}`), 1024, time.Time{}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("unknown field error = %v", err)
	}
	if _, err := DecodeCommand(payload, 1024, time.Date(2026, 9, 17, 2, 0, 0, 0, time.UTC)); !errors.Is(err, ErrCommandExpired) {
		t.Fatalf("expired error = %v", err)
	}
}
