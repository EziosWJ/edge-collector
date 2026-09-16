package acquisition

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition/script"
)

func TestChannelRunnerRunsAfterPollBeforeNextDeviceAndHoldsChannelDuringDelay(t *testing.T) {
	store := NewCurrentStateStore()
	session := &scriptRuntimeSessionFake{
		registers: map[uint8][]uint16{1: {101}, 2: {202}},
	}
	channel := Channel{ID: 701, Protocol: ProtocolModbusRTU, Enabled: Enabled}
	scriptID := int64(77)
	devices := []Device{
		{ID: 1, Name: "设备 1", ChannelID: channel.ID, UnitID: 1, ScriptID: &scriptID, Enabled: Enabled, PollIntervalMS: 60000, RegisterBlocks: []RegisterBlock{{ID: 7011, FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 100, Quantity: 1}}},
		{ID: 2, Name: "设备 2", ChannelID: channel.ID, UnitID: 2, ScriptID: &scriptID, Enabled: Enabled, PollIntervalMS: 60000, RegisterBlocks: []RegisterBlock{{ID: 7012, FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 100, Quantity: 1}}},
	}
	executor := &scriptExecutorFake{}
	firstScriptStarted := make(chan struct{}, 1)
	secondScriptStarted := make(chan struct{}, 1)
	executor.execute = func(ctx context.Context, _ script.ScriptVersion, invocation script.Invocation, host script.Host) (script.Result, error) {
		if invocation.DeviceID == 1 {
			if value, ok := host.RawRegister(FunctionCodeReadHoldingRegisters, 100); !ok || value != 101 {
				return script.Result{}, fmt.Errorf("raw register = %d, %v", value, ok)
			}
			firstScriptStarted <- struct{}{}
			if err := host.Delay(ctx, 80*time.Millisecond); err != nil {
				return script.Result{}, err
			}
		} else {
			secondScriptStarted <- struct{}{}
		}
		return script.Result{}, nil
	}

	runtime, err := NewRuntimeWithScripts([]Channel{channel}, devices, store, func(Channel, Device) (ModbusSession, error) {
		return session, nil
	}, nil, RuntimeScriptConfig{
		VersionProvider: func(_ context.Context, device Device) (*ScriptVersion, error) {
			return &ScriptVersion{ScriptID: scriptID, ID: device.ID + 7000, VersionNo: 1, Source: "def after_poll(ctx): pass"}, nil
		},
		Executor: executor,
	}, func(context.Context) ([]Channel, []Device, error) {
		return []Channel{channel}, devices, nil
	})
	if err != nil {
		t.Fatalf("NewRuntimeWithScripts() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	select {
	case <-firstScriptStarted:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("first after_poll did not start")
	}
	if reads := session.readEvents(); len(reads) != 1 {
		t.Fatalf("reads while first script delay is active = %v, want only first device", reads)
	}

	select {
	case <-secondScriptStarted:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("second device did not run after first script completed")
	}
	if reads := session.readEvents(); len(reads) != 2 {
		t.Fatalf("reads after both devices = %v, want two static reads", reads)
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runtime error = %v, want context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not stop")
	}
}

func TestRuntimeRefreshFreezesPublishedVersionAcrossPollCycles(t *testing.T) {
	store := NewCurrentStateStore()
	channel := Channel{ID: 7010, Protocol: ProtocolModbusRTU, Enabled: Enabled}
	scriptID := int64(7011)
	device := Device{
		ID: 7012, ChannelID: channel.ID, UnitID: 1, ScriptID: &scriptID,
		Enabled: Enabled, PollIntervalMS: 1,
		RegisterBlocks: []RegisterBlock{{ID: 7013, FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 10, Quantity: 1}},
	}
	var mu sync.Mutex
	versionCalls := 0
	version := ScriptVersion{ID: 7014, ScriptID: scriptID, VersionNo: 1, Source: "def after_poll(ctx): pass"}
	provider := func(context.Context, Device) (*ScriptVersion, error) {
		mu.Lock()
		defer mu.Unlock()
		versionCalls++
		if versionCalls > 1 {
			return nil, errors.New("published version was queried again")
		}
		return &version, nil
	}
	seen := make(chan int64, 3)
	executor := scriptExecutorFunc(func(_ context.Context, version script.ScriptVersion, _ script.Invocation, _ script.Host) (script.Result, error) {
		seen <- version.VersionID
		return script.Result{}, nil
	})
	runtime, err := NewRuntimeWithScripts(
		[]Channel{channel}, []Device{device}, store,
		func(Channel, Device) (ModbusSession, error) {
			return &scriptRuntimeSessionFake{registers: map[uint8][]uint16{1: {1}}}, nil
		}, nil,
		RuntimeScriptConfig{VersionProvider: provider, Executor: executor},
		func(context.Context) ([]Channel, []Device, error) {
			return []Channel{channel}, []Device{device}, nil
		},
	)
	if err != nil {
		t.Fatalf("NewRuntimeWithScripts() error = %v", err)
	}
	if err := runtime.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("runtime did not stop")
		}
	}()

	for index := 0; index < 3; index++ {
		select {
		case got := <-seen:
			if got != version.ID {
				t.Fatalf("poll cycle %d used version %d, want %d", index, got, version.ID)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("poll cycle %d did not execute the frozen published version", index)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if versionCalls != 1 {
		t.Fatalf("published version provider calls = %d, want 1 per refresh", versionCalls)
	}
}

func TestScriptDynamicOperationsShareChannelPacer(t *testing.T) {
	store := NewCurrentStateStore()
	underlying := &scriptRuntimeSessionFake{registers: map[uint8][]uint16{1: {42}}}
	var logs bytes.Buffer
	pacer := newRequestPacer(50 * time.Millisecond)
	clock := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	waits := make([]time.Duration, 0, 3)
	pacer.now = func() time.Time { return clock }
	pacer.sleep = func(_ context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		clock = clock.Add(duration)
		return nil
	}
	session := newPacedSessionWithPacer(underlying, pacer)
	device := Device{
		ID: 702, ChannelID: 1, UnitID: 1, ScriptID: int64Pointer(7020), Enabled: Enabled,
		RegisterBlocks: []RegisterBlock{{ID: 7021, FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 10, Quantity: 1}},
	}
	version := script.ScriptVersion{ScriptID: 7020, VersionID: 7021, VersionNo: 1, Source: "def after_poll(ctx): pass"}
	executor := scriptExecutorFunc(func(_ context.Context, _ script.ScriptVersion, _ script.Invocation, host script.Host) (script.Result, error) {
		if _, err := host.ReadHolding(context.Background(), 20, 1); err != nil {
			return script.Result{}, err
		}
		if err := host.WriteRegisters(context.Background(), 30, []uint16{7, 8}); err != nil {
			return script.Result{}, err
		}
		if err := host.WriteCoil(context.Background(), 40, true); err != nil {
			return script.Result{}, err
		}
		return script.Result{}, nil
	})
	runtime, err := NewRuntimeWithScripts(nil, nil, store, func(Channel, Device) (ModbusSession, error) {
		return underlying, nil
	}, log.New(&logs, "", 0), RuntimeScriptConfig{Executor: executor})
	if err != nil {
		t.Fatalf("NewRuntimeWithScripts() error = %v", err)
	}
	result := runtime.executeDeviceCycle(context.Background(), Channel{ID: 1}, device, session, false, capturedScriptCycle{
		invocation: script.Invocation{DeviceID: device.ID, ChannelID: device.ChannelID, UnitID: device.UnitID},
		version:    version,
		executor:   executor,
		enabled:    true,
	})
	if result.staticErr != nil || result.scriptErr != nil {
		t.Fatalf("cycle result = %#v, want successful static and script execution", result)
	}
	if len(waits) != 3 {
		t.Fatalf("pacer waits = %v, want one wait before each dynamic request", waits)
	}
	for index, wait := range waits {
		if wait != 50*time.Millisecond {
			t.Errorf("wait %d = %v, want 50ms", index, wait)
		}
	}
	want := []string{"unit:1", "read:1:10:1", "read:1:20:1", "write_registers:1:30:2", "write_coil:1:40:true"}
	if got := underlying.eventsSnapshot(); !equalStrings(got, want) {
		t.Fatalf("Modbus operations = %v, want %v", got, want)
	}
	for _, wantLog := range []string{
		"device_id=702 script_id=7020 script_version_id=7021 version_no=1 channel_id=1 function_code=3 address=20 quantity=1 result=OK",
		"device_id=702 script_id=7020 script_version_id=7021 version_no=1 channel_id=1 function_code=16 address=30 quantity=2 result=OK",
		"device_id=702 script_id=7020 script_version_id=7021 version_no=1 channel_id=1 function_code=5 address=40 quantity=1 result=OK",
	} {
		if !strings.Contains(logs.String(), wantLog) {
			t.Errorf("structured Modbus log missing %q in %q", wantLog, logs.String())
		}
	}
}

func TestScriptExecutionRunsInParallelAcrossChannels(t *testing.T) {
	store := NewCurrentStateStore()
	channels := []Channel{
		{ID: 710, Protocol: ProtocolModbusRTU, Enabled: Enabled},
		{ID: 711, Protocol: ProtocolModbusRTU, Enabled: Enabled},
	}
	scriptIDs := []int64{7100, 7110}
	devices := []Device{
		{ID: 7101, ChannelID: channels[0].ID, UnitID: 1, ScriptID: &scriptIDs[0], Enabled: Enabled, PollIntervalMS: 60000},
		{ID: 7111, ChannelID: channels[1].ID, UnitID: 1, ScriptID: &scriptIDs[1], Enabled: Enabled, PollIntervalMS: 60000},
	}
	started := make(chan int64, len(devices))
	release := make(chan struct{})
	executor := scriptExecutorFunc(func(ctx context.Context, _ script.ScriptVersion, invocation script.Invocation, _ script.Host) (script.Result, error) {
		started <- invocation.ChannelID
		select {
		case <-release:
			return script.Result{}, nil
		case <-ctx.Done():
			return script.Result{}, ctx.Err()
		}
	})
	runtime, err := NewRuntimeWithScripts(nil, nil, store, func(Channel, Device) (ModbusSession, error) {
		return &scriptRuntimeSessionFake{}, nil
	}, nil, RuntimeScriptConfig{
		VersionProvider: func(_ context.Context, device Device) (*ScriptVersion, error) {
			return &ScriptVersion{ScriptID: *device.ScriptID, ID: device.ID + 100, VersionNo: 1}, nil
		},
		Executor: executor,
	})
	if err != nil {
		t.Fatalf("NewRuntimeWithScripts() error = %v", err)
	}
	runners := []*channelRunner{
		newChannelRunner(runtime, channelConfig{
			active: true, channel: channels[0], devices: []Device{devices[0]},
			scriptVersions: map[int64]ScriptVersion{devices[0].ID: {ScriptID: scriptIDs[0], ID: devices[0].ID + 100, VersionNo: 1}},
		}),
		newChannelRunner(runtime, channelConfig{
			active: true, channel: channels[1], devices: []Device{devices[1]},
			scriptVersions: map[int64]ScriptVersion{devices[1].ID: {ScriptID: scriptIDs[1], ID: devices[1].ID + 100, VersionNo: 1}},
		}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, len(runners))
	for _, runner := range runners {
		go func(runner *channelRunner) {
			runner.Run(ctx)
			done <- struct{}{}
		}(runner)
	}
	defer cancel()

	seen := make(map[int64]bool, len(devices))
	for range devices {
		select {
		case channelID := <-started:
			seen[channelID] = true
		case <-time.After(2 * time.Second):
			for _, runner := range runners {
				runner.Stop()
			}
			t.Fatal("scripts on different channels did not start in parallel")
		}
	}
	if len(seen) != len(channels) {
		t.Fatalf("started channels = %v, want %v", seen, []int64{channels[0].ID, channels[1].ID})
	}

	close(release)
	for _, runner := range runners {
		runner.Stop()
	}
	for range runners {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("channel runner did not stop")
		}
	}
}

func TestScriptErrorDoesNotPolluteStaticState(t *testing.T) {
	store := NewCurrentStateStore()
	underlying := &scriptRuntimeSessionFake{registers: map[uint8][]uint16{1: {1234}}}
	session := newPacedSession(underlying, 0)
	scriptID := int64(7030)
	device := Device{
		ID: 703, ChannelID: 1, UnitID: 1, ScriptID: &scriptID, Enabled: Enabled,
		FailureThreshold: 1,
		RegisterBlocks:   []RegisterBlock{{ID: 7031, FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 10, Quantity: 1}},
	}
	expected := errors.New("script assertion failed")
	executor := scriptExecutorFunc(func(_ context.Context, _ script.ScriptVersion, _ script.Invocation, host script.Host) (script.Result, error) {
		if value, ok := host.RawRegister(FunctionCodeReadHoldingRegisters, 10); !ok || value != 1234 {
			return script.Result{}, fmt.Errorf("raw register = %d, %v", value, ok)
		}
		return script.Result{}, expected
	})
	runtime, err := NewRuntimeWithScripts(nil, nil, store, func(Channel, Device) (ModbusSession, error) {
		return underlying, nil
	}, nil, RuntimeScriptConfig{Executor: executor})
	if err != nil {
		t.Fatalf("NewRuntimeWithScripts() error = %v", err)
	}
	result := runtime.executeDeviceCycle(context.Background(), Channel{ID: 1}, device, session, false, capturedScriptCycle{
		invocation: script.Invocation{DeviceID: device.ID, ChannelID: device.ChannelID, UnitID: device.UnitID},
		version:    script.ScriptVersion{ScriptID: scriptID, VersionID: 1},
		executor:   executor,
		enabled:    true,
	})
	if !errors.Is(result.scriptErr, expected) {
		t.Fatalf("script error = %v, want %v", result.scriptErr, expected)
	}
	state, ok := store.Get(device.ID)
	if !ok || state.Status != StatusOnline || state.LastError != "" {
		t.Fatalf("static state = %#v, exists=%v, want online without script error", state, ok)
	}
	if state.RegisterBlocks[0].Values[0] == nil || *state.RegisterBlocks[0].Values[0] != 1234 {
		t.Fatalf("static register state = %#v, want 1234", state.RegisterBlocks)
	}
}

func TestRuntimePublishesScriptRuntimeObservation(t *testing.T) {
	store := NewCurrentStateStore()
	session := &scriptRuntimeSessionFake{registers: map[uint8][]uint16{1: {1234}}}
	channel := Channel{ID: 7030, Protocol: ProtocolModbusRTU, Enabled: Enabled}
	scriptID := int64(7031)
	device := Device{
		ID: 7032, Name: "观察设备", ChannelID: channel.ID, UnitID: 1, ScriptID: &scriptID,
		Enabled: Enabled, RegisterBlocks: []RegisterBlock{{ID: 7033, FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 10, Quantity: 1}},
	}
	runtimeScript := script.NewRuntime(script.Options{})
	runtime, err := NewRuntimeWithScripts(nil, nil, store, func(Channel, Device) (ModbusSession, error) {
		return session, nil
	}, nil, RuntimeScriptConfig{Executor: runtimeScript})
	if err != nil {
		t.Fatalf("NewRuntimeWithScripts() error = %v", err)
	}
	version := ScriptVersion{
		ID: 7034, ScriptID: scriptID, VersionNo: 1,
		Source: "def after_poll(ctx):\n    ctx.state_set(\"last\", 1)\n    ctx.emit_event(\"fault_detail\", \"index-0\", {\"registers\": [1, 2]})\n",
	}
	cycle := capturedScriptCycle{
		invocation: script.Invocation{DeviceID: device.ID, ChannelID: channel.ID, UnitID: device.UnitID, Protocol: channel.Protocol},
		version:    script.ScriptVersion{ScriptID: version.ScriptID, VersionID: version.ID, VersionNo: version.VersionNo, Source: version.Source},
		executor:   runtimeScript,
		enabled:    true,
	}
	result := runtime.executeDeviceCycle(context.Background(), channel, device, newPacedSession(session, 0), false, cycle)
	if result.scriptErr != nil {
		t.Fatalf("script error = %v", result.scriptErr)
	}
	runtime.recordScriptRuntimeState(device, cycle, result, time.Date(2026, 9, 16, 1, 2, 3, 0, time.UTC))

	observed, err := runtime.FindScriptRuntimeState(context.Background(), device.ID)
	if err != nil {
		t.Fatalf("FindScriptRuntimeState() error = %v", err)
	}
	if observed.ScriptID != scriptID || observed.ScriptVersionID != version.ID || observed.VersionNo != 1 {
		t.Fatalf("script identity = %+v", observed)
	}
	if observed.LastAttemptAt == nil || observed.LastSuccessAt == nil || !observed.LastAttemptAt.Equal(*observed.LastSuccessAt) {
		t.Fatalf("observation timestamps = %+v", observed)
	}
	if got := observed.State["last"]; got != int64(1) {
		t.Fatalf("observed state = %#v, want last=1", observed.State)
	}
	if len(observed.Events) != 1 || observed.Events[0].Kind != "fault_detail" || observed.Events[0].Key != "index-0" {
		t.Fatalf("observed events = %#v", observed.Events)
	}

	list, err := runtime.ListScriptRuntimeStates(context.Background())
	if err != nil || len(list) != 1 || list[0].DeviceID != device.ID {
		t.Fatalf("ListScriptRuntimeStates() = (%#v, %v)", list, err)
	}
}

func TestZNCKIFixtureScriptTriggersOnlyOnFaultEdges(t *testing.T) {
	store := NewCurrentStateStore()
	session := &znckSessionFake{detail: []uint16{42, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2026, 9, 16, 10, 30, 42, 1}}
	device := Device{
		ID: 7040, ChannelID: 7041, UnitID: 1, Enabled: Enabled,
		RegisterBlocks: []RegisterBlock{{ID: 7042, FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 8166, Quantity: 1}},
	}
	runtimeScript := script.NewRuntime(script.Options{})
	runtime, err := NewRuntimeWithScripts(nil, nil, store, func(Channel, Device) (ModbusSession, error) {
		return session, nil
	}, nil, RuntimeScriptConfig{Executor: runtimeScript})
	if err != nil {
		t.Fatalf("NewRuntimeWithScripts() error = %v", err)
	}
	source := `def after_poll(ctx):
    fault_code = ctx.raw_register(3, 8166)
    previous = ctx.state_get("fault_code", 0)
    if fault_code == 0:
        ctx.state_set("fault_code", 0)
    elif fault_code != previous:
        ctx.write_registers(8120, [0])
        ctx.delay(50)
        registers = ctx.read_holding(8121, 17)
        key = str(registers[0]) + "-" + str(registers[16])
        ctx.emit_event("fault_detail", key, {"index": 0, "registers": registers})
        ctx.state_set("fault_code", fault_code)
`
	version := ScriptVersion{ID: 7043, ScriptID: 7044, VersionNo: 1, Source: source}
	cycle := capturedScriptCycle{
		invocation: script.Invocation{DeviceID: device.ID, ChannelID: device.ChannelID, UnitID: device.UnitID, Protocol: ProtocolModbusTCP},
		version:    script.ScriptVersion{ScriptID: version.ScriptID, VersionID: version.ID, VersionNo: version.VersionNo, Source: version.Source},
		executor:   runtimeScript,
		enabled:    true,
	}
	channel := Channel{ID: device.ChannelID, Protocol: ProtocolModbusTCP}
	packet := func(fault uint16) {
		session.fault = fault
		result := runtime.executeDeviceCycle(context.Background(), channel, device, newPacedSession(session, 0), false, cycle)
		if result.staticErr != nil || result.scriptErr != nil {
			t.Fatalf("fault %d cycle result = %#v", fault, result)
		}
		runtime.recordScriptRuntimeState(device, cycle, result, time.Now().UTC())
	}

	packet(0)
	packet(7)
	packet(7)
	packet(8)
	packet(0)
	packet(7)

	got := session.eventsSnapshot()
	want := []string{
		"unit:1", "read:1:8166:1",
		"unit:1", "read:1:8166:1", "write_registers:1:8120:1", "read:1:8121:17",
		"unit:1", "read:1:8166:1",
		"unit:1", "read:1:8166:1", "write_registers:1:8120:1", "read:1:8121:17",
		"unit:1", "read:1:8166:1",
		"unit:1", "read:1:8166:1", "write_registers:1:8120:1", "read:1:8121:17",
	}
	if !equalStrings(got, want) {
		t.Fatalf("ZNCK-I request sequence = %v, want %v", got, want)
	}
	observed, err := runtime.FindScriptRuntimeState(context.Background(), device.ID)
	if err != nil {
		t.Fatalf("FindScriptRuntimeState() error = %v", err)
	}
	if got := observed.State["fault_code"]; got != int64(7) {
		t.Fatalf("final fault state = %#v, want 7", got)
	}
	if len(observed.Events) != 1 || observed.Events[0].Kind != "fault_detail" {
		t.Fatalf("final events = %#v, want the bounded raw-detail event", observed.Events)
	}
}

func TestDynamicTransportFailureClosesNetworkSession(t *testing.T) {
	store := NewCurrentStateStore()
	closed := make(chan struct{}, 1)
	underlying := &scriptRuntimeSessionFake{
		registers:         map[uint8][]uint16{1: {1}},
		writeRegisters:    errors.New("connection reset"),
		closeNotification: closed,
	}
	channel := Channel{ID: 704, Protocol: ProtocolModbusTCP, Enabled: Enabled}
	scriptID := int64(7040)
	device := Device{
		ID: 7041, ChannelID: channel.ID, UnitID: 1, ScriptID: &scriptID, Enabled: Enabled, PollIntervalMS: 60000,
		NetworkEndpoint: &NetworkEndpoint{Host: "192.0.2.1", Port: 502},
		RegisterBlocks:  []RegisterBlock{{ID: 7042, FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 10, Quantity: 1}},
	}
	executor := scriptExecutorFunc(func(ctx context.Context, _ script.ScriptVersion, _ script.Invocation, host script.Host) (script.Result, error) {
		return script.Result{}, host.WriteRegisters(ctx, 20, []uint16{1})
	})
	runtime, err := NewRuntimeWithScripts([]Channel{channel}, []Device{device}, store, func(Channel, Device) (ModbusSession, error) {
		return underlying, nil
	}, nil, RuntimeScriptConfig{
		VersionProvider: func(_ context.Context, device Device) (*ScriptVersion, error) {
			return &ScriptVersion{ScriptID: *device.ScriptID, ID: 7043, VersionNo: 1}, nil
		},
		Executor: executor,
	}, func(context.Context) ([]Channel, []Device, error) {
		return []Channel{channel}, []Device{device}, nil
	})
	if err != nil {
		t.Fatalf("NewRuntimeWithScripts() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("network session was not closed after dynamic transport failure")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runtime error = %v, want context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not stop")
	}
}

func TestScriptStateResetsWhenPublishedVersionChanges(t *testing.T) {
	store := NewCurrentStateStore()
	session := &scriptRuntimeSessionFake{registers: map[uint8][]uint16{1: {1}}}
	channel := Channel{ID: 705, Protocol: ProtocolModbusRTU, Enabled: Enabled}
	scriptID := int64(7050)
	device := Device{ID: 7051, ChannelID: channel.ID, UnitID: 1, ScriptID: &scriptID, Enabled: Enabled, PollIntervalMS: 1, RegisterBlocks: []RegisterBlock{{ID: 7052, FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 10, Quantity: 1}}}
	var mu sync.Mutex
	versionNo := 0
	versions := []int64{7053, 7054}
	first := make(chan struct{}, 1)
	second := make(chan struct{}, 1)
	resets := make(chan ScriptStateIdentity, 1)
	executor := &scriptExecutorFake{
		reset: resets,
		execute: func(_ context.Context, version script.ScriptVersion, _ script.Invocation, _ script.Host) (script.Result, error) {
			if version.VersionID == versions[0] {
				first <- struct{}{}
			} else if version.VersionID == versions[1] {
				second <- struct{}{}
			}
			return script.Result{}, nil
		},
	}
	runtime, err := NewRuntimeWithScripts([]Channel{channel}, []Device{device}, store, func(Channel, Device) (ModbusSession, error) {
		return session, nil
	}, nil, RuntimeScriptConfig{
		VersionProvider: func(_ context.Context, device Device) (*ScriptVersion, error) {
			mu.Lock()
			defer mu.Unlock()
			index := versionNo
			if index >= len(versions) {
				index = len(versions) - 1
			}
			versionNo++
			return &ScriptVersion{ScriptID: *device.ScriptID, ID: versions[index], VersionNo: index + 1}, nil
		},
		Executor: executor,
	}, func(context.Context) ([]Channel, []Device, error) {
		return []Channel{channel}, []Device{device}, nil
	})
	if err != nil {
		t.Fatalf("NewRuntimeWithScripts() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("version 1 script did not execute")
	}
	if err := runtime.Refresh(context.Background()); err != nil {
		cancel()
		t.Fatalf("Refresh() error = %v", err)
	}
	select {
	case identity := <-resets:
		if identity.ScriptVersionID != versions[0] {
			t.Fatalf("reset identity = %#v, want version %d", identity, versions[0])
		}
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("script state was not reset when published version changed")
	}
	select {
	case <-second:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("version 2 script did not execute")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runtime error = %v, want context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not stop")
	}
}

func TestScriptStateResetsWhenDeviceBindingChanges(t *testing.T) {
	store := NewCurrentStateStore()
	session := &scriptRuntimeSessionFake{registers: map[uint8][]uint16{1: {1}, 2: {2}}}
	channel := Channel{ID: 706, Protocol: ProtocolModbusRTU, Enabled: Enabled}
	oldScriptID := int64(7060)
	newScriptID := int64(7061)
	oldDevice := Device{ID: 7062, ChannelID: channel.ID, UnitID: 1, ScriptID: &oldScriptID, Enabled: Enabled, PollIntervalMS: 60000, RegisterBlocks: []RegisterBlock{{ID: 7063, FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 10, Quantity: 1}}}
	newDevice := oldDevice
	newDevice.ScriptID = &newScriptID
	started := make(chan struct{}, 1)
	resets := make(chan ScriptStateIdentity, 1)
	executor := &scriptExecutorFake{
		reset: resets,
		execute: func(_ context.Context, _ script.ScriptVersion, _ script.Invocation, _ script.Host) (script.Result, error) {
			started <- struct{}{}
			return script.Result{}, nil
		},
	}
	versionProvider := func(_ context.Context, device Device) (*ScriptVersion, error) {
		return &ScriptVersion{ScriptID: *device.ScriptID, ID: *device.ScriptID + 100, VersionNo: 1}, nil
	}
	runtime, err := NewRuntimeWithScripts([]Channel{channel}, []Device{oldDevice}, store, func(Channel, Device) (ModbusSession, error) {
		return session, nil
	}, nil, RuntimeScriptConfig{VersionProvider: versionProvider, Executor: executor})
	if err != nil {
		t.Fatalf("NewRuntimeWithScripts() error = %v", err)
	}
	runner := newChannelRunner(runtime, channelConfig{
		active: true, channel: channel, devices: []Device{oldDevice},
		scriptVersions: map[int64]ScriptVersion{oldDevice.ID: {ScriptID: oldScriptID, ID: oldScriptID + 100, VersionNo: 1}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("initial script did not execute")
	}
	runner.Update(channelConfig{
		active: true, channel: channel, devices: []Device{newDevice},
		scriptVersions: map[int64]ScriptVersion{newDevice.ID: {ScriptID: newScriptID, ID: newScriptID + 100, VersionNo: 1}},
	})
	select {
	case identity := <-resets:
		if identity.ScriptID != oldScriptID || identity.ScriptVersionID != oldScriptID+100 {
			t.Fatalf("reset identity = %#v, want old binding/version", identity)
		}
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("script state was not reset when device binding changed")
	}
	runner.Stop()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("channel runner did not stop")
	}
}

type scriptExecutorFunc func(context.Context, script.ScriptVersion, script.Invocation, script.Host) (script.Result, error)

func (f scriptExecutorFunc) Execute(ctx context.Context, version script.ScriptVersion, invocation script.Invocation, host script.Host) (script.Result, error) {
	return f(ctx, version, invocation, host)
}

type scriptExecutorFake struct {
	execute func(context.Context, script.ScriptVersion, script.Invocation, script.Host) (script.Result, error)
	reset   chan<- ScriptStateIdentity
}

func (f *scriptExecutorFake) Execute(ctx context.Context, version script.ScriptVersion, invocation script.Invocation, host script.Host) (script.Result, error) {
	if f.execute == nil {
		return script.Result{}, nil
	}
	return f.execute(ctx, version, invocation, host)
}

func (f *scriptExecutorFake) Reset(scope script.StateScope) {
	if f.reset != nil {
		f.reset <- ScriptStateIdentity{DeviceID: scope.DeviceID, ScriptID: scope.ScriptID, ScriptVersionID: scope.ScriptVersionID}
	}
}

type znckSessionFake struct {
	mu     sync.Mutex
	fault  uint16
	detail []uint16
	events []string
}

func (s *znckSessionFake) Open() error { return nil }

func (s *znckSessionFake) Close() error { return nil }

func (s *znckSessionFake) SetUnitID(unitID uint8) error {
	s.mu.Lock()
	s.events = append(s.events, fmt.Sprintf("unit:%d", unitID))
	s.mu.Unlock()
	return nil
}

func (s *znckSessionFake) ReadHoldingRegisters(_ context.Context, _ uint8, address, quantity uint16) ([]uint16, error) {
	s.mu.Lock()
	s.events = append(s.events, fmt.Sprintf("read:1:%d:%d", address, quantity))
	fault := s.fault
	detail := append([]uint16(nil), s.detail...)
	s.mu.Unlock()
	switch {
	case address == 8166 && quantity == 1:
		return []uint16{fault}, nil
	case address == 8121 && quantity == 17:
		return detail, nil
	default:
		return nil, fmt.Errorf("unexpected FC03 range %d/%d", address, quantity)
	}
}

func (s *znckSessionFake) ReadInputRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	return s.ReadHoldingRegisters(ctx, unitID, address, quantity)
}

func (s *znckSessionFake) WriteRegisters(_ context.Context, _ uint8, address uint16, values []uint16) error {
	s.mu.Lock()
	s.events = append(s.events, fmt.Sprintf("write_registers:1:%d:%d", address, len(values)))
	s.mu.Unlock()
	if address != 8120 || len(values) != 1 || values[0] != 0 {
		return fmt.Errorf("unexpected FC16 write %d/%v", address, values)
	}
	return nil
}

func (s *znckSessionFake) WriteCoil(_ context.Context, _ uint8, _ uint16, _ bool) error {
	return fmt.Errorf("unexpected FC05 write")
}

func (s *znckSessionFake) eventsSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

type scriptRuntimeSessionFake struct {
	mu                sync.Mutex
	current           uint8
	registers         map[uint8][]uint16
	readErrors        map[uint16]error
	writeRegisters    error
	writeCoil         error
	events            []string
	closeCount        int
	closeNotification chan<- struct{}
}

func (s *scriptRuntimeSessionFake) Open() error { return nil }

func (s *scriptRuntimeSessionFake) Close() error {
	s.mu.Lock()
	s.closeCount++
	s.mu.Unlock()
	if s.closeNotification != nil {
		s.closeNotification <- struct{}{}
	}
	return nil
}

func (s *scriptRuntimeSessionFake) SetUnitID(unitID uint8) error {
	s.mu.Lock()
	s.current = unitID
	s.events = append(s.events, fmt.Sprintf("unit:%d", unitID))
	s.mu.Unlock()
	return nil
}

func (s *scriptRuntimeSessionFake) ReadHoldingRegisters(_ context.Context, _ uint8, address, quantity uint16) ([]uint16, error) {
	s.mu.Lock()
	unitID := s.current
	s.events = append(s.events, fmt.Sprintf("read:%d:%d:%d", unitID, address, quantity))
	err := s.readErrors[address]
	values := append([]uint16(nil), s.registers[unitID]...)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	end := int(address) + int(quantity)
	if int(address) < 0 || quantity == 0 {
		return nil, fmt.Errorf("register range out of bounds")
	}
	if end > len(values) {
		if int(quantity) > len(values) {
			return nil, fmt.Errorf("register range out of bounds")
		}
		return values[:quantity], nil
	}
	return values[address:end], nil
}

func (s *scriptRuntimeSessionFake) ReadInputRegisters(ctx context.Context, unitID uint8, address, quantity uint16) ([]uint16, error) {
	return s.ReadHoldingRegisters(ctx, unitID, address, quantity)
}

func (s *scriptRuntimeSessionFake) WriteRegisters(_ context.Context, unitID uint8, address uint16, values []uint16) error {
	s.mu.Lock()
	s.events = append(s.events, fmt.Sprintf("write_registers:%d:%d:%d", unitID, address, len(values)))
	err := s.writeRegisters
	s.mu.Unlock()
	return err
}

func (s *scriptRuntimeSessionFake) WriteCoil(_ context.Context, unitID uint8, address uint16, on bool) error {
	s.mu.Lock()
	s.events = append(s.events, fmt.Sprintf("write_coil:%d:%d:%t", unitID, address, on))
	err := s.writeCoil
	s.mu.Unlock()
	return err
}

func (s *scriptRuntimeSessionFake) readEvents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]string, 0)
	for _, event := range s.events {
		if len(event) >= len("read:") && event[:len("read:")] == "read:" {
			result = append(result, event)
		}
	}
	return result
}

func (s *scriptRuntimeSessionFake) eventsSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func int64Pointer(value int64) *int64 { return &value }
