package acquisition

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/simonvetter/modbus"
)

func TestPollChannelOnceContinuesAfterOneDeviceFails(t *testing.T) {
	store := NewCurrentStateStore()
	session := &fakeModbusSession{
		registers: map[uint8][]uint16{
			1: {2200, 100, 0, 1000, 5000, 980, 1},
			3: {2210, 110, 0, 1100, 5000, 980, 1},
		},
		errors: map[uint8]error{2: errors.New("device timeout")},
	}
	devices := []Device{
		{ID: 1, Name: "设备 1", ChannelID: 9, UnitID: 1, DeviceType: DeviceTypeFeedProtector, Enabled: Enabled, FailureThreshold: 3, RegisterBlocks: testBlocks(11)},
		{ID: 2, Name: "设备 2", ChannelID: 9, UnitID: 2, DeviceType: DeviceTypeFeedProtector, Enabled: Enabled, FailureThreshold: 3, RegisterBlocks: testBlocks(12)},
		{ID: 3, Name: "设备 3", ChannelID: 9, UnitID: 3, DeviceType: DeviceTypeFeedProtector, Enabled: Enabled, FailureThreshold: 3, RegisterBlocks: testBlocks(13)},
	}

	if err := PollChannelOnce(context.Background(), Channel{ID: 9}, devices, session, store); err != nil {
		t.Fatalf("PollChannelOnce() error = %v, want device error isolated", err)
	}

	state1, ok := store.Get(1)
	if !ok || state1.Status != StatusOnline {
		t.Errorf("device 1 state = %#v, exists=%v, want online", state1, ok)
	}
	state2, ok := store.Get(2)
	if !ok || state2.Status != StatusDegraded {
		t.Errorf("device 2 state = %#v, exists=%v, want degraded", state2, ok)
	}
	state3, ok := store.Get(3)
	if !ok || state3.Status != StatusOnline {
		t.Errorf("device 3 state = %#v, exists=%v, want online after device 2 failure", state3, ok)
	}

	if got, want := session.units(), []uint8{1, 2, 3}; !equalUint8(got, want) {
		t.Errorf("slave call order = %v, want %v", got, want)
	}
}

func TestPollDeviceOnceReadsConfiguredBlocksInOrderAndKeepsGoing(t *testing.T) {
	store := NewCurrentStateStore()
	session := &fakeModbusSession{
		registers: map[uint8][]uint16{1: {100, 200, 300}},
		requestErrors: map[runtimeRegisterCall]error{
			{slaveID: 1, functionCode: FunctionCodeReadHoldingRegisters, address: 0, quantity: 2}: errors.New("holding timeout"),
		},
	}
	device := Device{
		ID: 20, Name: "设备 20", ChannelID: 9, UnitID: 1, DeviceType: DeviceTypeFeedProtector,
		FailureThreshold: 3, Enabled: Enabled, RegisterBlocks: []RegisterBlock{
			{ID: 201, Name: "保持", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 2, SortOrder: 0},
			{ID: 202, Name: "输入", FunctionCode: FunctionCodeReadInputRegisters, StartAddress: 0, Quantity: 1, SortOrder: 1},
		},
	}

	PollChannelOnce(context.Background(), Channel{ID: 9}, []Device{device}, session, store)
	state, ok := store.Get(device.ID)
	if !ok || state.Status != StatusDegraded {
		t.Fatalf("state = %#v, exists=%v, want degraded after one block failure", state, ok)
	}
	if state.RegisterBlocks[0].Valid || !state.RegisterBlocks[1].Valid || state.RegisterBlocks[1].Values[0] == nil || *state.RegisterBlocks[1].Values[0] != 100 {
		t.Fatalf("block states = %#v, want failed FC03 and successful FC04", state.RegisterBlocks)
	}
	want := []runtimeRegisterCall{
		{slaveID: 1, functionCode: FunctionCodeReadHoldingRegisters, address: 0, quantity: 2},
		{slaveID: 1, functionCode: FunctionCodeReadInputRegisters, address: 0, quantity: 1},
	}
	if !equalRuntimeRegisterCalls(session.calls, want) {
		t.Fatalf("register calls = %#v, want %#v", session.calls, want)
	}
}

func TestPollDeviceOnceDoesNotDispatchByDeviceType(t *testing.T) {
	store := NewCurrentStateStore()
	session := &fakeModbusSession{registers: map[uint8][]uint16{1: {42}}}
	device := Device{
		ID: 21, Name: "未建模设备", ChannelID: 9, UnitID: 1, DeviceType: "UNMODELED_DEVICE",
		FailureThreshold: 3, Enabled: Enabled,
		RegisterBlocks: []RegisterBlock{{ID: 211, Name: "原始块", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 1}},
	}

	if err := PollChannelOnce(context.Background(), Channel{ID: 9}, []Device{device}, session, store); err != nil {
		t.Fatalf("PollChannelOnce() error = %v", err)
	}
	state, ok := store.Get(device.ID)
	if !ok || state.Status != StatusOnline {
		t.Fatalf("state = %#v, exists=%v, want online for an unmodeled device", state, ok)
	}
	if value := state.RegisterBlocks[0].Values[0]; value == nil || *value != 42 {
		t.Fatalf("raw register value = %v, want 42", value)
	}
}

func TestNetworkDeviceFailureStopsRemainingBlocksForThatCycle(t *testing.T) {
	store := NewCurrentStateStore()
	first := RegisterBlock{ID: 301, Name: "首块", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 1}
	second := RegisterBlock{ID: 302, Name: "后续块", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 1, Quantity: 1}
	session := &fakeModbusSession{
		registers: map[uint8][]uint16{1: {100, 200}},
		requestErrors: map[runtimeRegisterCall]error{
			{slaveID: 1, functionCode: FunctionCodeReadHoldingRegisters, address: 0, quantity: 1}: errors.New("network transaction failed"),
		},
	}
	device := Device{ID: 30, Name: "网络设备", ChannelID: 9, UnitID: 1, Enabled: Enabled, FailureThreshold: 3, RegisterBlocks: []RegisterBlock{first, second}}

	err := pollDeviceCycle(context.Background(), Channel{ID: 9, Protocol: ProtocolModbusTCP}, device, session, store, true)
	if err == nil {
		t.Fatal("pollDeviceCycle() error = nil, want network transaction error")
	}
	if len(session.calls) != 1 {
		t.Fatalf("network calls = %#v, want only first block", session.calls)
	}
	state, ok := store.Get(device.ID)
	if !ok || state.RegisterBlocks[1].Valid {
		t.Fatalf("state = %#v, want unexecuted block invalid", state)
	}
}

func TestNetworkModbusExceptionKeepsReadingRemainingBlocks(t *testing.T) {
	store := NewCurrentStateStore()
	first := RegisterBlock{ID: 303, Name: "非法地址", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 1}
	second := RegisterBlock{ID: 304, Name: "后续块", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 1, Quantity: 1}
	session := &fakeModbusSession{
		registers: map[uint8][]uint16{1: {100, 200}},
		requestErrors: map[runtimeRegisterCall]error{
			{slaveID: 1, functionCode: FunctionCodeReadHoldingRegisters, address: 0, quantity: 1}: modbus.ErrIllegalDataAddress,
		},
	}
	device := Device{ID: 31, Name: "网络设备", ChannelID: 9, UnitID: 1, Enabled: Enabled, FailureThreshold: 3, RegisterBlocks: []RegisterBlock{first, second}}

	err := pollDeviceCycle(context.Background(), Channel{ID: 9, Protocol: ProtocolModbusTCP}, device, session, store, true)
	if err == nil {
		t.Fatal("pollDeviceCycle() error = nil, want Modbus exception to be reflected in device state")
	}
	if len(session.calls) != 2 {
		t.Fatalf("network calls = %#v, want exception block followed by remaining block", session.calls)
	}
	state, ok := store.Get(device.ID)
	if !ok || state.Status != StatusDegraded || state.RegisterBlocks[0].Valid || !state.RegisterBlocks[1].Valid {
		t.Fatalf("state = %#v, exists=%v, want degraded with second block valid", state, ok)
	}
}

func TestNetworkGatewayExceptionsKeepReadingRemainingBlocks(t *testing.T) {
	for _, gatewayError := range []error{modbus.ErrGWPathUnavailable, modbus.ErrGWTargetFailedToRespond} {
		t.Run(gatewayError.Error(), func(t *testing.T) {
			store := NewCurrentStateStore()
			first := RegisterBlock{ID: 305, Name: "网关异常", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 1}
			second := RegisterBlock{ID: 306, Name: "后续块", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 1, Quantity: 1}
			session := &fakeModbusSession{
				registers: map[uint8][]uint16{1: {100, 200}},
				requestErrors: map[runtimeRegisterCall]error{
					{slaveID: 1, functionCode: FunctionCodeReadHoldingRegisters, address: 0, quantity: 1}: gatewayError,
				},
			}
			device := Device{ID: 33, Name: "网关设备", ChannelID: 9, UnitID: 1, Enabled: Enabled, FailureThreshold: 3, RegisterBlocks: []RegisterBlock{first, second}}

			err := pollDeviceCycle(context.Background(), Channel{ID: 9, Protocol: ProtocolModbusTCP}, device, session, store, true)
			if err == nil || len(session.calls) != 2 {
				t.Fatalf("pollDeviceCycle() error=%v calls=%#v, want error and both blocks", err, session.calls)
			}
		})
	}
}

func TestRuntimeUsesIndependentNetworkSessionsPerDevice(t *testing.T) {
	store := NewCurrentStateStore()
	channel := Channel{ID: 90, Protocol: ProtocolModbusTCP, TimeoutMS: 100, Enabled: Enabled}
	devices := []Device{
		{ID: 91, Name: "TCP 设备 1", ChannelID: 90, UnitID: 1, NetworkEndpoint: &NetworkEndpoint{Host: "192.0.2.10", Port: 502}, Enabled: Enabled, PollIntervalMS: 60000, FailureThreshold: 3, RegisterBlocks: []RegisterBlock{{ID: 901, Name: "原始寄存器", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 1}}},
		{ID: 92, Name: "TCP 设备 2", ChannelID: 90, UnitID: 1, NetworkEndpoint: &NetworkEndpoint{Host: "192.0.2.11", Port: 502}, Enabled: Enabled, PollIntervalMS: 60000, FailureThreshold: 3, RegisterBlocks: []RegisterBlock{{ID: 902, Name: "原始寄存器", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 1}}},
	}
	var mu sync.Mutex
	factoryCalls := make(map[int64]int)
	factory := func(_ Channel, device Device) (ModbusSession, error) {
		mu.Lock()
		factoryCalls[device.ID]++
		mu.Unlock()
		return &fakeModbusSession{registers: map[uint8][]uint16{1: {42}}}, nil
	}
	runtime, err := NewRuntime([]Channel{channel}, devices, store, factory, nil)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	defer cancel()

	deadline := time.After(2 * time.Second)
	for {
		state1, ok1 := store.Get(91)
		state2, ok2 := store.Get(92)
		if ok1 && ok2 && state1.Status == StatusOnline && state2.Status == StatusOnline {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("network devices did not both become online: %#v %#v", state1, state2)
		case <-time.After(5 * time.Millisecond):
		}
	}
	mu.Lock()
	if factoryCalls[91] != 1 || factoryCalls[92] != 1 {
		mu.Unlock()
		t.Fatalf("factory calls = %#v, want one independent session per device", factoryCalls)
	}
	mu.Unlock()
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

func TestRuntimePacesNetworkRequestsAcrossDevices(t *testing.T) {
	store := NewCurrentStateStore()
	channel := Channel{ID: 93, Protocol: ProtocolModbusTCP, TimeoutMS: 100, InterRequestDelayMS: 30, Enabled: Enabled}
	devices := []Device{
		{ID: 94, Name: "TCP 设备 1", ChannelID: channel.ID, UnitID: 1, NetworkEndpoint: &NetworkEndpoint{Host: "192.0.2.20", Port: 502}, Enabled: Enabled, PollIntervalMS: 60000, FailureThreshold: 3, RegisterBlocks: testBlocks(940)},
		{ID: 95, Name: "TCP 设备 2", ChannelID: channel.ID, UnitID: 1, NetworkEndpoint: &NetworkEndpoint{Host: "192.0.2.21", Port: 502}, Enabled: Enabled, PollIntervalMS: 60000, FailureThreshold: 3, RegisterBlocks: testBlocks(950)},
	}
	readTimes := make(chan time.Time, len(devices))
	factory := func(Channel, Device) (ModbusSession, error) {
		return &fakeModbusSession{registers: map[uint8][]uint16{1: {1, 2, 3, 4, 5, 6, 7}}, readTimes: readTimes}, nil
	}
	runtime, err := NewRuntime([]Channel{channel}, devices, store, factory, nil)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	first := <-readTimes
	second := <-readTimes
	if gap := second.Sub(first); gap < 20*time.Millisecond {
		t.Fatalf("network request gap = %v, want channel pacing delay", gap)
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

func TestActiveChannelConfigsIncludesDevicesWithoutRegisterBlocks(t *testing.T) {
	configs := activeChannelConfigs(
		[]Channel{{ID: 9, Enabled: Enabled}},
		[]Device{
			{ID: 1, ChannelID: 9, Enabled: Enabled},
			{ID: 2, ChannelID: 9, Enabled: Enabled, RegisterBlocks: testBlocks(21)},
		},
	)

	config, ok := configs[9]
	if !ok {
		t.Fatal("active channel config missing, want configured device")
	}
	if len(config.devices) != 2 || config.devices[0].ID != 1 || config.devices[1].ID != 2 {
		t.Fatalf("active devices = %#v, want enabled devices including unconfigured device", config.devices)
	}
}

func TestRuntimeRefreshRemovesDisabledDeviceState(t *testing.T) {
	store := NewCurrentStateStore()
	session := &fakeModbusSession{
		registers: map[uint8][]uint16{
			1: {2200, 100, 0, 1000, 5000, 980, 1},
		},
	}
	channel := Channel{ID: 9, Enabled: Enabled}
	device := Device{ID: 1, Name: "设备 1", ChannelID: 9, UnitID: 1, DeviceType: DeviceTypeFeedProtector, Enabled: Enabled, PollIntervalMS: 60000, FailureThreshold: 3, RegisterBlocks: testBlocks(11)}

	var mu sync.Mutex
	enabled := true
	loader := func(context.Context) ([]Channel, []Device, error) {
		mu.Lock()
		defer mu.Unlock()
		if !enabled {
			return nil, nil, nil
		}
		return []Channel{channel}, []Device{device}, nil
	}
	runtime, err := NewRuntime([]Channel{channel}, []Device{device}, store, func(Channel, Device) (ModbusSession, error) {
		return session, nil
	}, nil, loader)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for {
		if state, ok := store.Get(device.ID); ok && state.Status == StatusOnline {
			break
		}
		select {
		case <-deadline:
			t.Fatal("device did not become online")
		case <-time.After(5 * time.Millisecond):
		}
	}

	mu.Lock()
	enabled = false
	mu.Unlock()
	if err := runtime.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	deadline = time.After(2 * time.Second)
	for {
		if _, ok := store.Get(device.ID); !ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("disabled device state was not removed")
		case <-time.After(5 * time.Millisecond):
		}
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

func TestRuntimeRefreshWaitsForInFlightDeviceRead(t *testing.T) {
	store := NewCurrentStateStore()
	readStarted := make(chan struct{}, 1)
	readRelease := make(chan struct{})
	session := &fakeModbusSession{
		registers:   map[uint8][]uint16{1: {100}},
		readStarted: readStarted,
		readRelease: readRelease,
	}
	channel := Channel{ID: 9, Enabled: Enabled}
	first := Device{ID: 1, Name: "旧设备", ChannelID: 9, UnitID: 1, DeviceType: DeviceTypeFeedProtector, Enabled: Enabled, PollIntervalMS: 60000, FailureThreshold: 3, RegisterBlocks: testBlocks(31)}
	second := Device{ID: 2, Name: "新设备", ChannelID: 9, UnitID: 1, DeviceType: DeviceTypeFeedProtector, Enabled: Enabled, PollIntervalMS: 60000, FailureThreshold: 3, RegisterBlocks: testBlocks(32)}
	runtime, err := NewRuntime([]Channel{channel}, []Device{first}, store, func(Channel, Device) (ModbusSession, error) {
		return session, nil
	}, nil)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	runner := newChannelRunner(runtime, channelConfig{active: true, channel: channel, devices: []Device{first}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()

	select {
	case <-readStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("initial read did not start")
	}
	runner.Update(channelConfig{active: true, channel: channel, devices: []Device{second}})
	time.Sleep(50 * time.Millisecond)
	if _, ok := store.Get(second.ID); ok {
		t.Fatal("configuration applied before in-flight read completed")
	}

	close(readRelease)
	deadline := time.After(2 * time.Second)
	for {
		if _, ok := store.Get(second.ID); ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("configuration was not applied after in-flight read completed")
		case <-time.After(time.Millisecond):
		}
	}
	runner.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("channel runner did not stop")
	}
}

func TestRuntimeRefreshMoveDoesNotRemoveStateOwnedByNewChannel(t *testing.T) {
	store := NewCurrentStateStore()
	channelA := Channel{ID: 40, Protocol: ProtocolModbusRTU, Enabled: Enabled, SerialConfig: &SerialConfig{Port: "/dev/ttyUSB0", BaudRate: 19200, DataBits: 8, StopBits: 1, Parity: "N"}}
	channelB := Channel{ID: 41, Protocol: ProtocolModbusRTU, Enabled: Enabled, SerialConfig: &SerialConfig{Port: "/dev/ttyUSB1", BaudRate: 19200, DataBits: 8, StopBits: 1, Parity: "N"}}
	device := Device{
		ID: 42, Name: "迁移设备", ChannelID: channelA.ID, UnitID: 1, DeviceType: DeviceTypeFeedProtector,
		Enabled: Enabled, PollIntervalMS: 60000, FailureThreshold: 3,
		RegisterBlocks: []RegisterBlock{{ID: 420, Name: "原始块", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 1}},
	}
	var mu sync.Mutex
	moved := false
	loader := func(context.Context) ([]Channel, []Device, error) {
		mu.Lock()
		defer mu.Unlock()
		if moved {
			updated := device
			updated.ChannelID = channelB.ID
			return []Channel{channelB}, []Device{updated}, nil
		}
		return []Channel{channelA}, []Device{device}, nil
	}
	factory := func(Channel, Device) (ModbusSession, error) {
		return &fakeModbusSession{registers: map[uint8][]uint16{1: {42}}}, nil
	}
	runtime, err := NewRuntime([]Channel{channelA}, []Device{device}, store, factory, nil, loader)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	waitForState := func(channelID int64) CurrentState {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			state, ok := store.Get(device.ID)
			if ok && state.ChannelID == channelID && state.Status == StatusOnline {
				return state
			}
			select {
			case <-deadline:
				t.Fatalf("device did not become online on channel %d: %#v", channelID, state)
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	waitForState(channelA.ID)

	mu.Lock()
	moved = true
	mu.Unlock()
	if err := runtime.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	waitForState(channelB.ID)
	time.Sleep(50 * time.Millisecond)
	state, ok := store.Get(device.ID)
	if !ok || state.ChannelID != channelB.ID {
		t.Fatalf("moved device state = %#v, exists=%v, want state owned by new channel", state, ok)
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

type fakeModbusSession struct {
	mu            sync.Mutex
	current       uint8
	called        []uint8
	calls         []runtimeRegisterCall
	registers     map[uint8][]uint16
	errors        map[uint8]error
	requestErrors map[runtimeRegisterCall]error
	readStarted   chan<- struct{}
	readRelease   <-chan struct{}
	readTimes     chan<- time.Time
}

func (s *fakeModbusSession) Open() error  { return nil }
func (s *fakeModbusSession) Close() error { return nil }

func (s *fakeModbusSession) SetUnitID(id uint8) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = id
	s.called = append(s.called, id)
	return nil
}

func (s *fakeModbusSession) ReadHoldingRegisters(_ context.Context, _ uint8, address, quantity uint16) ([]uint16, error) {
	if s.readTimes != nil {
		s.readTimes <- time.Now()
	}
	s.mu.Lock()
	id := s.current
	call := runtimeRegisterCall{slaveID: id, functionCode: FunctionCodeReadHoldingRegisters, address: address, quantity: quantity}
	s.calls = append(s.calls, call)
	s.mu.Unlock()
	if s.readStarted != nil {
		s.readStarted <- struct{}{}
		<-s.readRelease
	}
	if err := s.requestErrors[call]; err != nil {
		return nil, err
	}
	if err := s.errors[id]; err != nil {
		return nil, err
	}
	registers := s.registers[id]
	end := int(address) + int(quantity)
	if int(address) < 0 || end > len(registers) {
		return nil, errors.New("register range out of bounds")
	}
	return append([]uint16(nil), registers[address:end]...), nil
}

func (s *fakeModbusSession) ReadInputRegisters(ctx context.Context, slaveID uint8, address, quantity uint16) ([]uint16, error) {
	if s.readTimes != nil {
		s.readTimes <- time.Now()
	}
	s.mu.Lock()
	call := runtimeRegisterCall{slaveID: s.current, functionCode: FunctionCodeReadInputRegisters, address: address, quantity: quantity}
	s.calls = append(s.calls, call)
	s.mu.Unlock()
	if err := s.requestErrors[call]; err != nil {
		return nil, err
	}
	if err := s.errors[slaveID]; err != nil {
		return nil, err
	}
	registers := s.registers[slaveID]
	end := int(address) + int(quantity)
	if end > len(registers) {
		return nil, errors.New("register range out of bounds")
	}
	return append([]uint16(nil), registers[address:end]...), nil
}

type runtimeRegisterCall struct {
	slaveID      uint8
	functionCode int
	address      uint16
	quantity     uint16
}

func equalRuntimeRegisterCalls(left, right []runtimeRegisterCall) bool {
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

func testBlocks(id int64) []RegisterBlock {
	return []RegisterBlock{{ID: id, Name: "原始寄存器", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 7}}
}

func (s *fakeModbusSession) units() []uint8 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uint8(nil), s.called...)
}

func equalUint8(left, right []uint8) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
