package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition/script"
)

const (
	CommandErrorInvalidMessage                  = "INVALID_MESSAGE"
	CommandErrorDeviceNotFound                  = "DEVICE_NOT_FOUND"
	CommandErrorDeviceDisabled                  = "DEVICE_DISABLED"
	CommandErrorNoScriptBound                   = "NO_SCRIPT_BOUND"
	CommandErrorNoCommandHandler                = "NO_COMMAND_HANDLER"
	CommandErrorExpired                         = "EXPIRED"
	CommandErrorCommandIDConflict               = "COMMAND_ID_CONFLICT"
	CommandErrorQueueFull                       = "QUEUE_FULL"
	CommandErrorReliableResultCapacityExhausted = "RELIABLE_RESULT_CAPACITY_EXHAUSTED"
	CommandErrorCommandRuntimeUnavailable       = "COMMAND_RUNTIME_UNAVAILABLE"
	CommandErrorCommandRecoveryUncertain        = "COMMAND_RECOVERY_UNCERTAIN"
)

type CommandDeviceResolver interface {
	FindDeviceByExternalID(context.Context, string) (*acquisition.Device, error)
}

type CommandAcquisitionRuntime interface {
	ValidateCommandTarget(context.Context, int64, string) (acquisition.Device, acquisition.ScriptVersion, error)
	EnqueueCommand(context.Context, acquisition.CommandRequest) (*acquisition.CommandFuture, error)
}

type CommandIntake struct {
	repository *Repository
	devices    CommandDeviceResolver
	runtime    CommandAcquisitionRuntime
	builder    TopicBuilder
	maxBytes   int
	now        func() time.Time
}

func NewCommandIntake(repository *Repository, devices CommandDeviceResolver, runtime CommandAcquisitionRuntime, builder TopicBuilder) *CommandIntake {
	return &CommandIntake{repository: repository, devices: devices, runtime: runtime, builder: builder, maxBytes: DefaultMaxCommandBytes, now: func() time.Time { return time.Now().UTC() }}
}

func (i *CommandIntake) SetMaxPayloadBytes(maxBytes int) {
	if maxBytes < 1 {
		return
	}
	i.maxBytes = maxBytes
}

func (i *CommandIntake) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	i.now = now
}

// Handle is the MQTT callback's application boundary. It parses, validates,
// journals and enqueues only; Modbus sessions remain owned by acquisition
// channel runners.
func (i *CommandIntake) Handle(ctx context.Context, topic string, payload []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if i.repository == nil || i.devices == nil || i.runtime == nil {
		return errors.New(CommandErrorCommandRuntimeUnavailable)
	}
	topicDeviceID, err := i.builder.ParseCommandTopic(topic)
	if err != nil {
		return ErrInvalidMessage
	}
	now := i.clockNow()
	command, decodeErr := DecodeCommand(payload, i.maxBytes, now)
	if errors.Is(decodeErr, ErrInvalidMessage) {
		return ErrInvalidMessage
	}
	if command.DeviceID != topicDeviceID {
		return ErrInvalidMessage
	}
	hash, err := CanonicalCommandHash(command)
	if err != nil {
		return ErrInvalidMessage
	}
	existing, err := i.repository.LookupCommand(ctx, command.CommandID, hash)
	if err != nil {
		return err
	}
	if existing != nil {
		return i.handleDuplicate(ctx, command, existing)
	}
	if errors.Is(decodeErr, ErrCommandExpired) {
		return i.reject(ctx, command, hash, CommandStatusExpired, &WireError{Type: CommandErrorExpired, Message: "command expired"})
	}
	if decodeErr != nil {
		return ErrInvalidMessage
	}

	device, err := i.devices.FindDeviceByExternalID(ctx, command.DeviceID)
	if errors.Is(err, acquisition.ErrNotFound) || device == nil {
		return i.reject(ctx, command, hash, CommandStatusRejected, &WireError{Type: CommandErrorDeviceNotFound, Message: "device not found"})
	}
	if err != nil {
		return err
	}
	if device.Enabled != acquisition.Enabled {
		return i.reject(ctx, command, hash, CommandStatusRejected, &WireError{Type: CommandErrorDeviceDisabled, Message: "device is disabled"})
	}
	if device.ScriptID == nil {
		return i.reject(ctx, command, hash, CommandStatusRejected, &WireError{Type: CommandErrorNoScriptBound, Message: "device has no published script"})
	}
	if _, _, err := i.runtime.ValidateCommandTarget(ctx, device.ID, command.Name); err != nil {
		return i.reject(ctx, command, hash, CommandStatusRejected, i.platformWireError(err))
	}

	return i.admitAndEnqueue(ctx, command, hash, *device)
}

func (i *CommandIntake) admitAndEnqueue(ctx context.Context, command CommandInput, hash string, device acquisition.Device) error {
	now := i.clockNow()
	acceptedPayload, err := i.buildResult(command, CommandStatusAccepted, now, nil, nil, nil)
	if err != nil {
		return err
	}
	acceptedCommandID := command.CommandID
	acceptedExpiry := i.outboxExpiry(ctx, now)
	journal := CommandJournal{CommandID: command.CommandID, DeviceID: command.DeviceID, CommandName: command.Name, PayloadHash: hash, CommandPayload: string(commandPayload(command)), ReceivedAt: now, IssuedAt: command.IssuedAt, ExpiresAt: command.ExpiresAt, Status: CommandStatusAccepted}
	accepted := OutboxMessage{MessageID: envelopeMessageID(acceptedPayload), MessageType: OutboxMessageTypeCommandAccepted, CommandID: &acceptedCommandID, Topic: i.resultTopic(command.DeviceID), QoS: 1, Retain: 0, Payload: string(acceptedPayload), PayloadBytes: int64(len(acceptedPayload)), Priority: OutboxPriorityCommandNotice, CreatedAt: now, ExpiresAt: acceptedExpiry}
	reservation := FinalReservation{CommandID: command.CommandID, ReservedRows: 1, ReservedBytes: DefaultResultReservationBytes, AcceptedBytes: accepted.PayloadBytes, FinalBytes: DefaultResultReservationBytes, CreatedAt: now}
	admission, err := i.repository.AdmitCommand(ctx, journal, accepted, reservation)
	if err != nil {
		return err
	}
	if admission.Existing != nil {
		return i.handleDuplicate(ctx, command, admission.Existing)
	}

	var startedMu sync.Mutex
	var startedAt *time.Time
	future, err := i.runtime.EnqueueCommand(ctx, acquisition.CommandRequest{
		CommandID: command.CommandID,
		DeviceID:  device.ID,
		Name:      command.Name,
		Args:      command.Args,
		ExpiresAt: command.ExpiresAt,
		OnStarted: func() error {
			at := i.clockNow()
			if at.After(command.ExpiresAt) {
				return acquisition.ErrCommandExpired
			}
			changed, markErr := i.repository.MarkCommandRunning(context.Background(), command.CommandID, at)
			if markErr != nil {
				return markErr
			}
			if !changed {
				return acquisition.ErrCommandNotRunnable
			}
			startedMu.Lock()
			startedAt = &at
			startedMu.Unlock()
			return nil
		},
	})
	if err != nil {
		return i.finalizeFailure(ctx, command, acquisition.CommandExecutionResult{}, nil, i.platformWireError(err))
	}
	go func() {
		result, waitErr := future.Wait(context.Background())
		startedMu.Lock()
		started := cloneTimePointer(startedAt)
		startedMu.Unlock()
		_ = i.finalizeExecution(context.Background(), command, result, waitErr, started, nil)
	}()
	return nil
}

func (i *CommandIntake) reject(ctx context.Context, command CommandInput, hash, status string, wireError *WireError) error {
	now := i.clockNow()
	completed := now
	payload, err := i.buildResult(command, status, now, nil, nil, wireError)
	if err != nil {
		return err
	}
	commandID := command.CommandID
	journal := CommandJournal{CommandID: command.CommandID, DeviceID: command.DeviceID, CommandName: command.Name, PayloadHash: hash, CommandPayload: string(commandPayload(command)), ReceivedAt: now, IssuedAt: command.IssuedAt, ExpiresAt: command.ExpiresAt, Status: status, CompletedAt: &completed, ResultPayload: string(payload), ErrorType: stringPointer(wireError.Type), ErrorMessage: stringPointer(wireError.Message)}
	final := OutboxMessage{MessageID: envelopeMessageID(payload), MessageType: OutboxMessageTypeCommandResult, CommandID: &commandID, Topic: i.resultTopic(command.DeviceID), QoS: 1, Retain: 0, Payload: string(payload), PayloadBytes: int64(len(payload)), Priority: OutboxPriorityCommandFinal, CreatedAt: now, ExpiresAt: i.outboxExpiry(ctx, now)}
	admission, err := i.repository.AdmitRejectedCommand(ctx, journal, final)
	if err != nil {
		return err
	}
	if admission.Existing != nil {
		return i.handleDuplicate(ctx, command, admission.Existing)
	}
	return nil
}

func (i *CommandIntake) handleDuplicate(ctx context.Context, command CommandInput, existing *CommandJournal) error {
	if !isTerminalCommandStatus(existing.Status) {
		return nil
	}
	return i.repository.RequeueStoredFinal(ctx, command.CommandID, i.resultTopic(command.DeviceID))
}

func (i *CommandIntake) finalizeFailure(ctx context.Context, command CommandInput, result acquisition.CommandExecutionResult, started *time.Time, wireError *WireError) error {
	return i.finalizeExecution(ctx, command, result, errors.New(wireError.Type), started, wireError)
}

func (i *CommandIntake) finalizeExecution(ctx context.Context, command CommandInput, execution acquisition.CommandExecutionResult, executionErr error, startedAt *time.Time, forcedWireError *WireError) error {
	now := i.clockNow()
	status := CommandStatusSucceeded
	wireError := forcedWireError
	resultValue := execution.Result.Output
	if executionErr != nil {
		status = CommandStatusFailed
		if wireError == nil {
			wireError = i.executionWireError(executionErr)
		}
		resultValue = nil
		if errors.Is(executionErr, acquisition.ErrCommandExpired) {
			status = CommandStatusExpired
			wireError = &WireError{Type: CommandErrorExpired, Message: "command expired before execution"}
		}
	}
	completed := now
	payload, err := i.buildResult(command, status, now, startedAt, &completed, resultValue, wireError)
	if err != nil || int64(len(payload)) > DefaultResultReservationBytes {
		status = CommandStatusFailed
		wireError = &WireError{Type: string(script.ErrorClassScriptOutput), Message: "command result is too large"}
		resultValue = nil
		payload, err = i.buildResult(command, status, now, startedAt, &completed, resultValue, wireError)
	}
	if err != nil {
		return err
	}
	commandID := command.CommandID
	errorType, errorMessage := (*string)(nil), (*string)(nil)
	if wireError != nil {
		errorType, errorMessage = stringPointer(wireError.Type), stringPointer(wireError.Message)
	}
	journal := CommandJournal{CommandID: command.CommandID, DeviceID: command.DeviceID, CommandName: command.Name, Status: status, StartedAt: cloneTimePointer(startedAt), CompletedAt: &completed, ResultPayload: string(payload), ErrorType: errorType, ErrorMessage: errorMessage}
	final := OutboxMessage{MessageID: envelopeMessageID(payload), MessageType: OutboxMessageTypeCommandResult, CommandID: &commandID, Topic: i.resultTopic(command.DeviceID), QoS: 1, Retain: 0, Payload: string(payload), PayloadBytes: int64(len(payload)), Priority: OutboxPriorityCommandFinal, CreatedAt: now, ExpiresAt: i.outboxExpiry(ctx, now)}
	return i.repository.FinalizeCommand(ctx, journal, final)
}

func (i *CommandIntake) Recover(ctx context.Context) error {
	commands, err := i.repository.ListIncompleteCommands(ctx)
	if err != nil {
		return err
	}
	for _, journal := range commands {
		if journal.CommandPayload == "" {
			if err := i.finalizeRecovered(ctx, journal, CommandStatusFailed, &WireError{Type: CommandErrorCommandRecoveryUncertain, Message: "command payload unavailable after restart"}); err != nil {
				return err
			}
			continue
		}
		command, decodeErr := DecodeCommand([]byte(journal.CommandPayload), i.maxBytes, i.clockNow())
		if decodeErr != nil && !errors.Is(decodeErr, ErrCommandExpired) {
			if err := i.finalizeRecovered(ctx, journal, CommandStatusFailed, &WireError{Type: CommandErrorInvalidMessage, Message: "stored command is invalid"}); err != nil {
				return err
			}
			continue
		}
		if journal.Status == CommandStatusRunning {
			if err := i.finalizeRecovered(ctx, journal, CommandStatusFailed, &WireError{Type: CommandErrorCommandRecoveryUncertain, Message: "command execution state is unknown after restart"}); err != nil {
				return err
			}
			continue
		}
		if errors.Is(decodeErr, ErrCommandExpired) || i.clockNow().After(command.ExpiresAt) {
			if err := i.finalizeRecovered(ctx, journal, CommandStatusExpired, &WireError{Type: CommandErrorExpired, Message: "command expired"}); err != nil {
				return err
			}
			continue
		}
		device, findErr := i.devices.FindDeviceByExternalID(ctx, command.DeviceID)
		if findErr != nil || device == nil || device.Enabled != acquisition.Enabled || device.ScriptID == nil {
			wireError := i.platformWireError(findErr)
			if device == nil || errors.Is(findErr, acquisition.ErrNotFound) {
				wireError = &WireError{Type: CommandErrorDeviceNotFound, Message: "device not found"}
			} else if device.Enabled != acquisition.Enabled {
				wireError = &WireError{Type: CommandErrorDeviceDisabled, Message: "device is disabled"}
			} else if device.ScriptID == nil {
				wireError = &WireError{Type: CommandErrorNoScriptBound, Message: "device has no published script"}
			}
			if err := i.finalizeRecovered(ctx, journal, CommandStatusFailed, wireError); err != nil {
				return err
			}
			continue
		}
		if _, _, validateErr := i.runtime.ValidateCommandTarget(ctx, device.ID, command.Name); validateErr != nil {
			if err := i.finalizeRecovered(ctx, journal, CommandStatusFailed, i.platformWireError(validateErr)); err != nil {
				return err
			}
			continue
		}
		if err := i.admitRecovered(ctx, journal, command, *device); err != nil {
			return err
		}
	}
	return nil
}

func (i *CommandIntake) admitRecovered(ctx context.Context, journal CommandJournal, command CommandInput, device acquisition.Device) error {
	var startedMu sync.Mutex
	var startedAt *time.Time
	future, err := i.runtime.EnqueueCommand(ctx, acquisition.CommandRequest{CommandID: command.CommandID, DeviceID: device.ID, Name: command.Name, Args: command.Args, ExpiresAt: command.ExpiresAt, OnStarted: func() error {
		at := i.clockNow()
		if at.After(command.ExpiresAt) {
			return acquisition.ErrCommandExpired
		}
		changed, err := i.repository.MarkCommandRunning(context.Background(), command.CommandID, at)
		if err != nil {
			return err
		}
		if !changed {
			return acquisition.ErrCommandNotRunnable
		}
		startedMu.Lock()
		startedAt = &at
		startedMu.Unlock()
		return nil
	}})
	if err != nil {
		return i.finalizeRecovered(ctx, journal, CommandStatusFailed, i.platformWireError(err))
	}
	go func() {
		result, waitErr := future.Wait(context.Background())
		startedMu.Lock()
		started := cloneTimePointer(startedAt)
		startedMu.Unlock()
		_ = i.finalizeExecution(context.Background(), command, result, waitErr, started, nil)
	}()
	return nil
}

func (i *CommandIntake) finalizeRecovered(ctx context.Context, journal CommandJournal, status string, wireError *WireError) error {
	command := CommandInput{Schema: SchemaDeviceCommand, CommandID: journal.CommandID, DeviceID: journal.DeviceID, Name: journal.CommandName, IssuedAt: journal.IssuedAt, ExpiresAt: journal.ExpiresAt}
	now := i.clockNow()
	completed := now
	payload, err := i.buildResult(command, status, now, journal.StartedAt, &completed, nil, wireError)
	if err != nil {
		return err
	}
	commandID := command.CommandID
	errorType, errorMessage := stringPointer(wireError.Type), stringPointer(wireError.Message)
	journal.Status = status
	journal.CompletedAt = &completed
	journal.ResultPayload = string(payload)
	journal.ErrorType = errorType
	journal.ErrorMessage = errorMessage
	final := OutboxMessage{MessageID: envelopeMessageID(payload), MessageType: OutboxMessageTypeCommandResult, CommandID: &commandID, Topic: i.resultTopic(command.DeviceID), QoS: 1, Retain: 0, Payload: string(payload), PayloadBytes: int64(len(payload)), Priority: OutboxPriorityCommandFinal, CreatedAt: now, ExpiresAt: i.outboxExpiry(ctx, now)}
	return i.repository.FinalizeCommand(ctx, journal, final)
}

func (i *CommandIntake) buildResult(command CommandInput, status string, at time.Time, startedAt, completedAt *time.Time, values ...any) ([]byte, error) {
	var result any
	var wireError *WireError
	if len(values) > 0 {
		result = values[0]
	}
	if len(values) > 1 {
		wireError, _ = values[1].(*WireError)
	}
	return BuildCommandResult(i.builder.EdgeID(), command.DeviceID, NewMessageID(at), command, status, at, startedAt, completedAt, result, wireError, at)
}

func (i *CommandIntake) resultTopic(deviceID string) string {
	topic, _ := i.builder.DeviceCommandResult(deviceID)
	return topic
}

func (i *CommandIntake) outboxExpiry(ctx context.Context, at time.Time) *time.Time {
	config, err := i.repository.GetConfig(ctx)
	if err != nil {
		return nil
	}
	expires := at.Add(time.Duration(config.OutboxRetentionDays) * 24 * time.Hour)
	return &expires
}

func (i *CommandIntake) clockNow() time.Time {
	if i.now == nil {
		return time.Now().UTC()
	}
	return i.now().UTC()
}

func (i *CommandIntake) platformWireError(err error) *WireError {
	if errors.Is(err, ErrReliableResultCapacityExhausted) {
		return &WireError{Type: CommandErrorReliableResultCapacityExhausted, Message: "reliable final result capacity exhausted"}
	}
	if errors.Is(err, ErrCommandJournalCapacity) {
		return &WireError{Type: CommandErrorReliableResultCapacityExhausted, Message: "command journal capacity exhausted"}
	}
	if errors.Is(err, acquisition.ErrCommandQueueFull) {
		return &WireError{Type: CommandErrorQueueFull, Message: "command queue is full"}
	}
	if errors.Is(err, acquisition.ErrCommandUnavailable) {
		return &WireError{Type: CommandErrorNoCommandHandler, Message: "command handler is unavailable"}
	}
	if errors.Is(err, acquisition.ErrCommandRuntimeStopped) {
		return &WireError{Type: CommandErrorCommandRuntimeUnavailable, Message: "command runtime is unavailable"}
	}
	if errors.Is(err, acquisition.ErrCommandTargetUnavailable) || errors.Is(err, acquisition.ErrNotFound) {
		return &WireError{Type: CommandErrorDeviceNotFound, Message: "device not found"}
	}
	if errors.Is(err, acquisition.ErrCommandNotRunnable) {
		return &WireError{Type: CommandErrorCommandRuntimeUnavailable, Message: "command execution is unavailable"}
	}
	return i.executionWireError(err)
}

func (i *CommandIntake) executionWireError(err error) *WireError {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &WireError{Type: string(script.ErrorClassCanceled), Message: "command canceled"}
	}
	if class := script.ClassOf(err); class != "" {
		return &WireError{Type: string(class), Message: stableScriptErrorMessage(class)}
	}
	return &WireError{Type: CommandErrorCommandRuntimeUnavailable, Message: "command execution failed"}
}

func stableScriptErrorMessage(class script.ErrorClass) string {
	switch class {
	case script.ErrorClassScriptCompile:
		return "script compilation failed"
	case script.ErrorClassScriptRuntime:
		return "script runtime failed"
	case script.ErrorClassScriptLimit:
		return "script limit exceeded"
	case script.ErrorClassScriptOutput:
		return "script output is invalid"
	case script.ErrorClassModbusTransport:
		return "modbus transport failed"
	case script.ErrorClassModbusException:
		return "modbus exception"
	case script.ErrorClassCanceled:
		return "command canceled"
	default:
		return "command execution failed"
	}
}

func commandPayload(command CommandInput) []byte {
	payload, err := canonicalCommandPayload(command)
	if err != nil {
		return nil
	}
	return payload
}

func canonicalCommandPayload(command CommandInput) ([]byte, error) {
	return canonicalJSON(map[string]any{"schema": command.Schema, "commandId": command.CommandID, "deviceId": command.DeviceID, "name": command.Name, "args": command.Args, "issuedAt": formatInstant(command.IssuedAt), "expiresAt": formatInstant(command.ExpiresAt)})
}

func envelopeMessageID(payload []byte) string {
	var envelope Envelope
	if err := json.Unmarshal(payload, &envelope); err == nil && envelope.MessageID != "" {
		return envelope.MessageID
	}
	return NewMessageID(time.Now().UTC())
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
