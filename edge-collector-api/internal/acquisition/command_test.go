package acquisition

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition/script"
)

func TestChannelRunnerExecutesCommandOnlyAfterCompletePoll(t *testing.T) {
	store := NewCurrentStateStore()
	session := &scriptRuntimeSessionFake{registers: map[uint8][]uint16{1: {42}}}
	channel := Channel{ID: 9001, Protocol: ProtocolModbusRTU, Enabled: Enabled}
	scriptID := int64(9002)
	device := Device{ID: 9003, ChannelID: channel.ID, UnitID: 1, ScriptID: &scriptID, Enabled: Enabled, PollIntervalMS: 60000, RegisterBlocks: []RegisterBlock{{ID: 9004, FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 10, Quantity: 1}}}
	executor := &commandExecutorFake{afterPoll: make(chan struct{}, 1), command: make(chan struct{}, 1)}
	runtime, err := NewRuntimeWithScripts([]Channel{channel}, []Device{device}, store, func(Channel, Device) (ModbusSession, error) {
		return session, nil
	}, nil, RuntimeScriptConfig{
		VersionProvider: func(context.Context, Device) (*ScriptVersion, error) {
			return &ScriptVersion{ID: 9005, ScriptID: scriptID, VersionNo: 1}, nil
		},
		Executor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := newChannelRunner(runtime, channelConfig{active: true, channel: channel, devices: []Device{device}, scriptVersions: map[int64]ScriptVersion{device.ID: {ID: 9005, ScriptID: scriptID, VersionNo: 1}}})
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		runner.Run(ctx)
		close(done)
	}()
	commandDone := make(chan CommandExecutionResult, 1)
	if err := runner.EnqueueCommand(queuedCommand{request: CommandRequest{CommandID: "command-9006", DeviceID: device.ID, Name: "set_value", Args: map[string]any{"value": int64(7)}}, done: commandDone}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.afterPoll:
	case <-time.After(2 * time.Second):
		t.Fatal("poll did not complete")
	}
	select {
	case result := <-commandDone:
		if result.Err != nil {
			t.Fatalf("command result error = %v", result.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("command did not execute after poll")
	}
	events := session.eventsSnapshot()
	if len(events) < 3 || events[0] != "unit:1" || events[1] != "read:1:10:1" || events[2] != "write_registers:1:20:1" {
		t.Fatalf("session events = %v, want poll before command write", events)
	}
	if len(executor.commandCalls) != 1 || executor.commandCalls[0] != "set_value" {
		t.Fatalf("command calls = %v", executor.commandCalls)
	}
	runner.Stop()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop")
	}
}

func TestCommandQueueIsBounded(t *testing.T) {
	runtime, err := NewRuntime(nil, nil, NewCurrentStateStore(), func(Channel, Device) (ModbusSession, error) {
		return nil, errors.New("unused")
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := newChannelRunner(runtime, channelConfig{})
	runner.SetCommandQueueCapacity(1)
	command := queuedCommand{request: CommandRequest{DeviceID: 1}, done: make(chan CommandExecutionResult, 1)}
	if err := runner.EnqueueCommand(command); err != nil {
		t.Fatal(err)
	}
	if err := runner.EnqueueCommand(command); !errors.Is(err, ErrCommandQueueFull) {
		t.Fatalf("second enqueue error = %v, want queue full", err)
	}
	runner.Stop()
}

func TestCommandCapturesLatestPublishedVersionAtSafeBoundary(t *testing.T) {
	store := NewCurrentStateStore()
	channel := Channel{ID: 9011, Protocol: ProtocolModbusRTU, Enabled: Enabled}
	scriptID := int64(9012)
	device := Device{ID: 9013, ChannelID: channel.ID, UnitID: 1, ScriptID: &scriptID, Enabled: Enabled}
	runtime, err := NewRuntimeWithScripts(nil, nil, store, func(Channel, Device) (ModbusSession, error) {
		return nil, errors.New("unused")
	}, nil, RuntimeScriptConfig{Executor: &commandExecutorFake{}})
	if err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.started = true
	runtime.scriptVersions = map[int64]ScriptVersion{device.ID: {ID: 9015, ScriptID: scriptID, VersionNo: 2}}
	runtime.mu.Unlock()

	cycle, err := runtime.captureLatestCommandCycle(channel, device, map[int64]ScriptVersion{device.ID: {ID: 9014, ScriptID: scriptID, VersionNo: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if cycle.version.VersionID != 9015 || cycle.version.VersionNo != 2 {
		t.Fatalf("command captured version = %#v, want latest published version", cycle.version)
	}
}

type commandExecutorFake struct {
	afterPoll    chan struct{}
	command      chan struct{}
	commandCalls []string
}

func (f *commandExecutorFake) Execute(context.Context, script.ScriptVersion, script.Invocation, script.Host) (script.Result, error) {
	if f.afterPoll != nil {
		f.afterPoll <- struct{}{}
	}
	return script.Result{}, nil
}

func (f *commandExecutorFake) CommandAvailable(context.Context, script.ScriptVersion) (bool, error) {
	return true, nil
}

func (f *commandExecutorFake) ExecuteCommand(ctx context.Context, _ script.ScriptVersion, _ script.Invocation, host script.Host, name string, _ any) (script.Result, error) {
	f.commandCalls = append(f.commandCalls, name)
	if f.command != nil {
		f.command <- struct{}{}
	}
	if err := host.WriteRegisters(ctx, 20, []uint16{7}); err != nil {
		return script.Result{}, err
	}
	return script.Result{Output: map[string]any{"name": name}}, nil
}
