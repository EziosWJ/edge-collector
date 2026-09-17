package mqtt

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition/script"
)

const (
	SchemaEdgeStatus       = "edge-status/v1"
	SchemaDeviceStatus     = "device-status/v1"
	SchemaRawSnapshot      = "raw-register-snapshot/v1"
	SchemaDeviceEvent      = "device-event/v1"
	SchemaDeviceCommand    = "device-command/v1"
	SchemaCommandResult    = "device-command-result/v1"
	DefaultMaxCommandBytes = DefaultCommandPayloadBytes
)

var (
	ErrInvalidMessage = errors.New("INVALID_MESSAGE")
	ErrCommandExpired = errors.New("EXPIRED")
)

type Envelope struct {
	Schema    string `json:"schema"`
	MessageID string `json:"messageId"`
	EdgeID    string `json:"edgeId"`
	DeviceID  string `json:"deviceId,omitempty"`
	Timestamp string `json:"timestamp"`
	Data      any    `json:"data"`
}

type CommandInput struct {
	Schema    string         `json:"schema"`
	CommandID string         `json:"commandId"`
	DeviceID  string         `json:"deviceId"`
	Name      string         `json:"name"`
	Args      map[string]any `json:"args"`
	IssuedAt  time.Time      `json:"issuedAt"`
	ExpiresAt time.Time      `json:"expiresAt"`
}

type WireError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type CommandResultData struct {
	CommandID   string     `json:"commandId"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	ReceivedAt  string     `json:"receivedAt"`
	StartedAt   *string    `json:"startedAt"`
	CompletedAt *string    `json:"completedAt"`
	Result      any        `json:"result"`
	Error       *WireError `json:"error"`
}

func NewMessageID(now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	random := make([]byte, 10)
	if _, err := rand.Read(random); err != nil {
		return fmt.Sprintf("%d", now.UTC().UnixNano())
	}
	return now.UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(random)
}

func BuildEdgeStatus(edgeID string, messageID string, at time.Time, online bool, reason string) ([]byte, error) {
	return json.Marshal(Envelope{Schema: SchemaEdgeStatus, MessageID: messageID, EdgeID: edgeID, Timestamp: formatInstant(at), Data: map[string]any{"online": online, "reason": reason}})
}

func BuildDeviceStatus(edgeID, deviceID, messageID string, state acquisition.CurrentState, at time.Time) ([]byte, error) {
	data := map[string]any{
		"status":        string(state.Status),
		"lastSuccessAt": nullableInstant(state.LastSuccessAt),
		"lastAttemptAt": nullableInstant(state.LastAttemptAt),
		"error":         nullableString(state.LastError),
	}
	return json.Marshal(Envelope{Schema: SchemaDeviceStatus, MessageID: messageID, EdgeID: edgeID, DeviceID: deviceID, Timestamp: formatInstant(at), Data: data})
}

func BuildRawSnapshot(edgeID, deviceID, messageID string, state acquisition.CurrentState, at time.Time) ([]byte, error) {
	blocks := append([]acquisition.RegisterBlockState(nil), state.RegisterBlocks...)
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].SortOrder != blocks[j].SortOrder {
			return blocks[i].SortOrder < blocks[j].SortOrder
		}
		return blocks[i].ID < blocks[j].ID
	})
	encodedBlocks := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		quantity := block.Quantity
		if quantity < 0 {
			quantity = 0
		}
		registers := make([]map[string]any, quantity)
		for index := range registers {
			var value *uint16
			if index < len(block.Values) {
				value = block.Values[index]
			}
			item := map[string]any{"address": block.StartAddress + index, "value": nil}
			if value != nil {
				item["value"] = int(*value)
			}
			registers[index] = item
		}
		encodedBlocks = append(encodedBlocks, map[string]any{
			"name":          block.Name,
			"functionCode":  block.FunctionCode,
			"valid":         block.Valid,
			"lastSuccessAt": nullableInstant(block.LastSuccessAt),
			"lastAttemptAt": nullableInstant(block.LastAttemptAt),
			"error":         nullableString(block.LastError),
			"registers":     registers,
		})
	}
	data := map[string]any{"communicationStatus": string(state.Status), "blocks": encodedBlocks}
	return json.Marshal(Envelope{Schema: SchemaRawSnapshot, MessageID: messageID, EdgeID: edgeID, DeviceID: deviceID, Timestamp: formatInstant(at), Data: data})
}

func BuildDeviceEvent(edgeID, deviceID, messageID string, event script.Event, version script.ScriptVersion, at time.Time) ([]byte, error) {
	if event.At.IsZero() {
		event.At = at
	}
	data := map[string]any{"kind": event.Kind, "key": event.Key, "scriptId": version.ScriptID, "scriptVersionId": version.VersionID, "payload": event.Payload}
	return json.Marshal(Envelope{Schema: SchemaDeviceEvent, MessageID: messageID, EdgeID: edgeID, DeviceID: deviceID, Timestamp: formatInstant(event.At), Data: data})
}

func BuildCommandResult(edgeID, deviceID, messageID string, command CommandInput, status string, receivedAt time.Time, startedAt, completedAt *time.Time, result any, wireError *WireError, at time.Time) ([]byte, error) {
	data := CommandResultData{CommandID: command.CommandID, Name: command.Name, Status: status, ReceivedAt: formatInstant(receivedAt), StartedAt: instantStringPointer(startedAt), CompletedAt: instantStringPointer(completedAt), Result: result, Error: wireError}
	return json.Marshal(Envelope{Schema: SchemaCommandResult, MessageID: messageID, EdgeID: edgeID, DeviceID: deviceID, Timestamp: formatInstant(at), Data: data})
}

func DecodeCommand(payload []byte, maxBytes int, now time.Time) (CommandInput, error) {
	if maxBytes < 1 {
		maxBytes = DefaultMaxCommandBytes
	}
	if len(payload) == 0 || len(payload) > maxBytes {
		return CommandInput{}, ErrInvalidMessage
	}
	var raw struct {
		Schema    string          `json:"schema"`
		CommandID string          `json:"commandId"`
		DeviceID  string          `json:"deviceId"`
		Name      string          `json:"name"`
		Args      json.RawMessage `json:"args"`
		IssuedAt  string          `json:"issuedAt"`
		ExpiresAt string          `json:"expiresAt"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return CommandInput{}, ErrInvalidMessage
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return CommandInput{}, ErrInvalidMessage
	}
	if raw.Schema != SchemaDeviceCommand || !validIdentitySegment(raw.CommandID) || !validIdentitySegment(raw.DeviceID) || !validIdentitySegment(raw.Name) || len(raw.Args) == 0 || raw.IssuedAt == "" || raw.ExpiresAt == "" {
		return CommandInput{}, ErrInvalidMessage
	}
	var args map[string]any
	argsDecoder := json.NewDecoder(bytes.NewReader(raw.Args))
	argsDecoder.UseNumber()
	if err := argsDecoder.Decode(&args); err != nil || args == nil {
		return CommandInput{}, ErrInvalidMessage
	}
	var trailing any
	if err := argsDecoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return CommandInput{}, ErrInvalidMessage
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, raw.IssuedAt)
	if err != nil {
		return CommandInput{}, ErrInvalidMessage
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, raw.ExpiresAt)
	if err != nil || !expiresAt.After(issuedAt) {
		return CommandInput{}, ErrInvalidMessage
	}
	command := CommandInput{Schema: raw.Schema, CommandID: raw.CommandID, DeviceID: raw.DeviceID, Name: raw.Name, Args: args, IssuedAt: issuedAt.UTC(), ExpiresAt: expiresAt.UTC()}
	if !now.IsZero() && now.UTC().After(command.ExpiresAt) {
		return command, ErrCommandExpired
	}
	return command, nil
}

func CanonicalCommandHash(command CommandInput) (string, error) {
	canonical := map[string]any{"schema": command.Schema, "commandId": command.CommandID, "deviceId": command.DeviceID, "name": command.Name, "args": command.Args, "issuedAt": formatInstant(command.IssuedAt), "expiresAt": formatInstant(command.ExpiresAt)}
	encoded, err := canonicalJSON(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalJSON(value any) ([]byte, error) {
	switch value := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var builder strings.Builder
		builder.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				builder.WriteByte(',')
			}
			encodedKey, err := json.Marshal(key)
			if err != nil {
				return nil, err
			}
			builder.Write(encodedKey)
			builder.WriteByte(':')
			encodedValue, err := canonicalJSON(value[key])
			if err != nil {
				return nil, err
			}
			builder.Write(encodedValue)
		}
		builder.WriteByte('}')
		return []byte(builder.String()), nil
	case []any:
		var builder strings.Builder
		builder.WriteByte('[')
		for index, item := range value {
			if index > 0 {
				builder.WriteByte(',')
			}
			encoded, err := canonicalJSON(item)
			if err != nil {
				return nil, err
			}
			builder.Write(encoded)
		}
		builder.WriteByte(']')
		return []byte(builder.String()), nil
	case json.Number:
		if value.String() == "" {
			return nil, ErrInvalidMessage
		}
		return []byte(value.String()), nil
	default:
		return json.Marshal(value)
	}
}

func formatInstant(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339Nano)
}

func nullableInstant(at *time.Time) any {
	if at == nil {
		return nil
	}
	return formatInstant(*at)
}

func instantStringPointer(at *time.Time) *string {
	if at == nil {
		return nil
	}
	value := formatInstant(*at)
	return &value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
