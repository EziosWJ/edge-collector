package script

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.starlark.net/starlark"
)

type invocationState struct {
	ctx        context.Context
	invocation Invocation
	version    ScriptVersion
	host       Host
	limits     Limits
	store      *stateStore
	scope      StateScope
	state      map[string]any
	events     []Event
	counter    ModbusOperationCounter
	now        func() time.Time
	printSink  PrintSink

	totalDelayMs int
	printLines   int
}

type contextValue struct {
	execution *invocationState
}

var _ starlark.HasAttrs = (*contextValue)(nil)

func (c *contextValue) String() string       { return "<device_context>" }
func (c *contextValue) Type() string         { return "device_context" }
func (c *contextValue) Freeze()              {}
func (c *contextValue) Truth() starlark.Bool { return starlark.True }
func (c *contextValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable type: %s", c.Type())
}

func (c *contextValue) Attr(name string) (starlark.Value, error) {
	s := c.execution
	switch name {
	case "device_id":
		return starlark.MakeInt64(s.invocation.DeviceID), nil
	case "channel_id":
		return starlark.MakeInt64(s.invocation.ChannelID), nil
	case "unit_id":
		return starlark.MakeInt(int(s.invocation.UnitID)), nil
	case "protocol":
		return starlark.String(s.invocation.Protocol), nil
	case "script_id":
		return starlark.MakeInt64(s.version.ScriptID), nil
	case "script_version_id":
		return starlark.MakeInt64(s.version.VersionID), nil
	case "script_version":
		return starlark.MakeInt(s.version.VersionNo), nil
	case "raw_register":
		return s.builtin("raw_register", s.rawRegister), nil
	case "read_holding":
		return s.builtin("read_holding", s.readHolding), nil
	case "write_registers":
		return s.builtin("write_registers", s.writeRegisters), nil
	case "write_coil":
		return s.builtin("write_coil", s.writeCoil), nil
	case "delay":
		return s.builtin("delay", s.delay), nil
	case "state_get":
		return s.builtin("state_get", s.stateGet), nil
	case "state_set":
		return s.builtin("state_set", s.stateSet), nil
	case "emit_event":
		return s.builtin("emit_event", s.emitEvent), nil
	default:
		return nil, nil
	}
}

func (c *contextValue) AttrNames() []string {
	names := []string{
		"channel_id",
		"device_id",
		"delay",
		"emit_event",
		"protocol",
		"raw_register",
		"read_holding",
		"script_id",
		"script_version",
		"script_version_id",
		"state_get",
		"state_set",
		"unit_id",
		"write_coil",
		"write_registers",
	}
	sort.Strings(names)
	return names
}

func (s *invocationState) builtin(name string, call func(starlark.Tuple, []starlark.Tuple) (starlark.Value, error)) *starlark.Builtin {
	return starlark.NewBuiltin("ctx."+name, func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		return call(args, kwargs)
	})
}

func (s *invocationState) requireHost(operation string) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if s.host == nil {
		return newClassifiedError(ErrorClassHostUnavailable, operation+": host is unavailable", nil)
	}
	return nil
}

func (s *invocationState) consumeModbus(operation string) error {
	if err := s.counter.Consume(); err != nil {
		return newClassifiedError(
			ErrorClassScriptLimit,
			fmt.Sprintf("%s: maximum Modbus operations (%d) exceeded", operation, s.limits.MaxModbusOperations),
			err,
		)
	}
	return nil
}

func (s *invocationState) rawRegister(args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var functionCode, address int
	if err := starlark.UnpackArgs("ctx.raw_register", args, kwargs,
		"function_code", &functionCode,
		"address", &address,
	); err != nil {
		return nil, err
	}
	if functionCode != 3 && functionCode != 4 {
		return nil, fmt.Errorf("ctx.raw_register: function_code must be 3 or 4")
	}
	if address < 0 || address > 65535 {
		return nil, fmt.Errorf("ctx.raw_register: address must be between 0 and 65535")
	}
	if err := s.requireHost("ctx.raw_register"); err != nil {
		return nil, err
	}
	value, ok := s.host.RawRegister(functionCode, uint16(address))
	if !ok {
		return starlark.None, nil
	}
	return starlark.MakeInt(int(value)), nil
}

func (s *invocationState) readHolding(args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var address, quantity int
	if err := starlark.UnpackArgs("ctx.read_holding", args, kwargs,
		"address", &address,
		"quantity", &quantity,
	); err != nil {
		return nil, err
	}
	if err := validateModbusRange(address, quantity, 125); err != nil {
		return nil, fmt.Errorf("ctx.read_holding: %w", err)
	}
	if err := s.requireHost("ctx.read_holding"); err != nil {
		return nil, err
	}
	if err := s.consumeModbus("ctx.read_holding"); err != nil {
		return nil, err
	}
	values, err := s.host.ReadHolding(s.ctx, uint16(address), uint16(quantity))
	if err != nil {
		return nil, hostFailure("ctx.read_holding", err, ErrorClassModbusTransport)
	}
	result := make([]starlark.Value, len(values))
	for i, value := range values {
		result[i] = starlark.MakeInt(int(value))
	}
	return starlark.NewList(result), nil
}

func (s *invocationState) writeRegisters(args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var address int
	var values starlark.Value
	if err := starlark.UnpackArgs("ctx.write_registers", args, kwargs,
		"address", &address,
		"values", &values,
	); err != nil {
		return nil, err
	}
	registers, err := unpackUint16List(values)
	if err != nil {
		return nil, fmt.Errorf("ctx.write_registers: %w", err)
	}
	if err := validateModbusRange(address, len(registers), 123); err != nil {
		return nil, fmt.Errorf("ctx.write_registers: %w", err)
	}
	if err := s.requireHost("ctx.write_registers"); err != nil {
		return nil, err
	}
	if err := s.consumeModbus("ctx.write_registers"); err != nil {
		return nil, err
	}
	if err := s.host.WriteRegisters(s.ctx, uint16(address), registers); err != nil {
		return nil, hostFailure("ctx.write_registers", err, ErrorClassModbusTransport)
	}
	return starlark.None, nil
}

func (s *invocationState) writeCoil(args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var address int
	var on bool
	if err := starlark.UnpackArgs("ctx.write_coil", args, kwargs,
		"address", &address,
		"on", &on,
	); err != nil {
		return nil, err
	}
	if address < 0 || address > 65535 {
		return nil, fmt.Errorf("ctx.write_coil: address must be between 0 and 65535")
	}
	if err := s.requireHost("ctx.write_coil"); err != nil {
		return nil, err
	}
	if err := s.consumeModbus("ctx.write_coil"); err != nil {
		return nil, err
	}
	if err := s.host.WriteCoil(s.ctx, uint16(address), on); err != nil {
		return nil, hostFailure("ctx.write_coil", err, ErrorClassModbusTransport)
	}
	return starlark.None, nil
}

func (s *invocationState) delay(args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var milliseconds int
	if err := starlark.UnpackArgs("ctx.delay", args, kwargs, "milliseconds", &milliseconds); err != nil {
		return nil, err
	}
	if milliseconds < 0 {
		return nil, fmt.Errorf("ctx.delay: milliseconds must be non-negative")
	}
	if milliseconds > s.limits.MaxDelayMs {
		return nil, newClassifiedError(
			ErrorClassScriptLimit,
			fmt.Sprintf("ctx.delay: %dms exceeds the per-delay limit of %dms", milliseconds, s.limits.MaxDelayMs),
			nil,
		)
	}
	if s.totalDelayMs > s.limits.MaxTotalDelayMs-milliseconds {
		return nil, newClassifiedError(
			ErrorClassScriptLimit,
			fmt.Sprintf("ctx.delay: cumulative delay exceeds the limit of %dms", s.limits.MaxTotalDelayMs),
			nil,
		)
	}
	if err := s.requireHost("ctx.delay"); err != nil {
		return nil, err
	}
	s.totalDelayMs += milliseconds
	if err := s.host.Delay(s.ctx, durationMillis(milliseconds)); err != nil {
		return nil, hostFailure("ctx.delay", err, ErrorClassScriptRuntime)
	}
	return starlark.None, nil
}

func (s *invocationState) stateGet(args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	var fallback starlark.Value = starlark.None
	if err := starlark.UnpackArgs("ctx.state_get", args, kwargs,
		"key", &key,
		"default?", &fallback,
	); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, fmt.Errorf("ctx.state_get: key must not be empty")
	}
	value, ok := s.state[key]
	if !ok {
		return fallback, nil
	}
	converted, err := toStarlarkValue(value)
	if err != nil {
		return nil, newClassifiedError(ErrorClassScriptOutput, "stored state cannot be read: "+err.Error(), err)
	}
	return converted, nil
}

func (s *invocationState) stateSet(args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	var value starlark.Value
	if err := starlark.UnpackArgs("ctx.state_set", args, kwargs,
		"key", &key,
		"value", &value,
	); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, fmt.Errorf("ctx.state_set: key must not be empty")
	}
	converted, err := starlarkToJSONValue(value, make(map[uintptr]struct{}))
	if err != nil {
		return nil, newClassifiedError(ErrorClassScriptOutput, "state value is not JSON-compatible: "+err.Error(), err)
	}
	candidate := cloneState(s.state)
	candidate[key] = converted
	size, err := jsonSize(candidate)
	if err != nil {
		return nil, newClassifiedError(ErrorClassScriptOutput, "state cannot be serialized: "+err.Error(), err)
	}
	if size > s.limits.MaxStateBytesPerDevice {
		return nil, newClassifiedError(
			ErrorClassScriptLimit,
			fmt.Sprintf("state size %d bytes exceeds the per-device limit of %d", size, s.limits.MaxStateBytesPerDevice),
			nil,
		)
	}
	s.state = candidate
	return starlark.None, nil
}

func (s *invocationState) emitEvent(args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var kind, key string
	var payload starlark.Value
	if err := starlark.UnpackArgs("ctx.emit_event", args, kwargs,
		"kind", &kind,
		"key", &key,
		"payload", &payload,
	); err != nil {
		return nil, err
	}
	if kind == "" || key == "" {
		return nil, fmt.Errorf("ctx.emit_event: kind and key must not be empty")
	}
	if len(s.events) >= s.limits.MaxEventsPerExecution {
		return nil, newClassifiedError(
			ErrorClassScriptLimit,
			fmt.Sprintf("event count exceeds the per-execution limit of %d", s.limits.MaxEventsPerExecution),
			nil,
		)
	}
	converted, err := starlarkToJSONValue(payload, make(map[uintptr]struct{}))
	if err != nil {
		return nil, newClassifiedError(ErrorClassScriptOutput, "event payload is not JSON-compatible: "+err.Error(), err)
	}
	size, err := jsonSize(converted)
	if err != nil {
		return nil, newClassifiedError(ErrorClassScriptOutput, "event payload cannot be serialized: "+err.Error(), err)
	}
	if size > s.limits.MaxEventPayloadBytes {
		return nil, newClassifiedError(
			ErrorClassScriptLimit,
			fmt.Sprintf("event payload size %d bytes exceeds the limit of %d", size, s.limits.MaxEventPayloadBytes),
			nil,
		)
	}
	s.events = append(s.events, Event{
		Kind:    kind,
		Key:     key,
		Payload: converted,
		At:      s.now(),
	})
	return starlark.None, nil
}

func (s *invocationState) print(args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	sep := " "
	if err := starlark.UnpackArgs("print", nil, kwargs, "sep?", &sep); err != nil {
		return nil, err
	}
	if s.printLines >= s.limits.MaxPrintLines {
		return nil, newClassifiedError(
			ErrorClassScriptLimit,
			fmt.Sprintf("print line count exceeds the limit of %d", s.limits.MaxPrintLines),
			nil,
		)
	}
	parts := make([]string, len(args))
	for i, value := range args {
		parts[i] = formatPrintValue(value)
	}
	message := strings.Join(parts, sep)
	if len([]byte(message)) > s.limits.MaxPrintLineBytes {
		return nil, newClassifiedError(
			ErrorClassScriptLimit,
			fmt.Sprintf("print line is %d bytes, maximum is %d", len([]byte(message)), s.limits.MaxPrintLineBytes),
			nil,
		)
	}
	s.printLines++
	if s.printSink != nil {
		s.printSink.Print(s.ctx, PrintEntry{
			Message:         message,
			DeviceID:        s.invocation.DeviceID,
			ScriptID:        s.version.ScriptID,
			ScriptVersionID: s.version.VersionID,
			VersionNo:       s.version.VersionNo,
			ChannelID:       s.invocation.ChannelID,
		})
	}
	return starlark.None, nil
}

func formatPrintValue(value starlark.Value) string {
	if value == nil {
		return "<nil>"
	}
	if stringValue, ok := starlark.AsString(value); ok {
		return stringValue
	}
	if bytesValue, ok := value.(starlark.Bytes); ok {
		return string(bytesValue)
	}
	return value.String()
}

func unpackUint16List(value starlark.Value) ([]uint16, error) {
	var length int
	var at func(int) starlark.Value
	switch value := value.(type) {
	case *starlark.List:
		if value == nil {
			return nil, fmt.Errorf("values must be a non-empty list")
		}
		length = value.Len()
		at = value.Index
	case starlark.Tuple:
		length = len(value)
		at = func(index int) starlark.Value { return value[index] }
	default:
		return nil, fmt.Errorf("values must be a non-empty list")
	}
	if length == 0 {
		return nil, fmt.Errorf("values must be a non-empty list")
	}
	registers := make([]uint16, length)
	for i := range registers {
		integer, ok := at(i).(starlark.Int)
		if !ok {
			return nil, fmt.Errorf("values[%d] must be an unsigned 16-bit integer", i)
		}
		value, ok := integer.Uint64()
		if !ok || value > 65535 {
			return nil, fmt.Errorf("values[%d] must be between 0 and 65535", i)
		}
		registers[i] = uint16(value)
	}
	return registers, nil
}

func validateModbusRange(address, quantity, maxQuantity int) error {
	if address < 0 || address > 65535 {
		return fmt.Errorf("address must be between 0 and 65535")
	}
	if quantity <= 0 {
		return fmt.Errorf("quantity must be positive")
	}
	if quantity > maxQuantity {
		return fmt.Errorf("quantity must be at most %d", maxQuantity)
	}
	if uint64(address)+uint64(quantity) > 65536 {
		return fmt.Errorf("address range exceeds 65535")
	}
	return nil
}

func hostFailure(operation string, err error, defaultClass ErrorClass) error {
	if err == nil {
		return nil
	}
	if classified := classifiedErrorInChain(err); classified != nil {
		copy := *classified
		copy.Message = operation + ": " + classified.Message
		copy.Cause = err
		return &copy
	}
	return newClassifiedError(defaultClass, operation+": "+err.Error(), err)
}
