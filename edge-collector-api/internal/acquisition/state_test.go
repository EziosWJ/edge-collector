package acquisition

import (
	"errors"
	"testing"
	"time"
)

func TestCurrentStateStoreKeepsFailedBlockValueAndMarksOffline(t *testing.T) {
	store := NewCurrentStateStore()
	device := Device{
		ID: 7, Name: "设备 7", ChannelID: 2, UnitID: 3, FailureThreshold: 3,
		RegisterBlocks: []RegisterBlock{
			{ID: 10, Name: "电气", FunctionCode: 3, StartAddress: 0, Quantity: 2, SortOrder: 0},
			{ID: 11, Name: "状态", FunctionCode: 4, StartAddress: 6, Quantity: 1, SortOrder: 1},
		},
	}
	firstAt := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	store.Ensure(device)
	store.RecordCycle(device, []RegisterBlockRead{
		{Block: device.RegisterBlocks[0], Values: []uint16{2200, 85}},
		{Block: device.RegisterBlocks[1], Values: []uint16{1}},
	}, firstAt)
	state, ok := store.Get(device.ID)
	if !ok || state.Status != StatusOnline {
		t.Fatalf("initial state = %#v, exists=%v, want online", state, ok)
	}
	if state.RegisterBlocks[0].Values[0] == nil || *state.RegisterBlocks[0].Values[0] != 2200 {
		t.Fatalf("initial values = %#v, want raw value 2200", state.RegisterBlocks[0].Values)
	}

	partialAt := firstAt.Add(time.Second)
	store.RecordCycle(device, []RegisterBlockRead{
		{Block: device.RegisterBlocks[0], Values: []uint16{2210, 90}},
		{Block: device.RegisterBlocks[1], Err: errors.New("input timeout")},
	}, partialAt)
	state, _ = store.Get(device.ID)
	if *state.RegisterBlocks[0].Values[0] != 2210 {
		t.Fatalf("successful block value = %#v, want 2210", state.RegisterBlocks[0].Values)
	}
	failed := state.RegisterBlocks[1]
	if failed.Valid || failed.LastError == "" {
		t.Fatalf("failed block = %#v, want invalid with error", failed)
	}
	if failed.Values[0] == nil || *failed.Values[0] != 1 {
		t.Fatalf("failed block value = %#v, want retained value 1", failed.Values)
	}
	if state.Status != StatusDegraded || state.ConsecutiveFailures != 0 {
		t.Fatalf("partial state = %#v, want degraded with broken all-failure streak", state)
	}

	for i := 0; i < 3; i++ {
		store.RecordCycle(device, []RegisterBlockRead{
			{Block: device.RegisterBlocks[0], Err: errors.New("holding timeout")},
			{Block: device.RegisterBlocks[1], Err: errors.New("input timeout")},
		}, partialAt.Add(time.Duration(i+2)*time.Second))
	}
	state, _ = store.Get(device.ID)
	if state.Status != StatusOffline || state.ConsecutiveFailures != 3 {
		t.Fatalf("offline state = %#v, want offline after three all-block failures", state)
	}

	recoveredAt := partialAt.Add(6 * time.Second)
	store.RecordCycle(device, []RegisterBlockRead{
		{Block: device.RegisterBlocks[0], Values: []uint16{2220, 95}},
		{Block: device.RegisterBlocks[1], Values: []uint16{2}},
	}, recoveredAt)
	state, _ = store.Get(device.ID)
	if state.Status != StatusOnline || state.ConsecutiveFailures != 0 {
		t.Fatalf("recovered state = %#v, want online with zero failures", state)
	}
	if state.LastSuccessAt == nil || !state.LastSuccessAt.Equal(recoveredAt) {
		t.Errorf("LastSuccessAt = %v, want %v", state.LastSuccessAt, recoveredAt)
	}
}

func TestCurrentStateStoreResetsOnlyStructuralBlockChanges(t *testing.T) {
	store := NewCurrentStateStore()
	device := Device{ID: 8, Name: "设备 8", RegisterBlocks: []RegisterBlock{{ID: 20, Name: "原始", FunctionCode: 3, StartAddress: 0, Quantity: 1}}}
	store.Ensure(device)
	when := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	store.RecordCycle(device, []RegisterBlockRead{{Block: device.RegisterBlocks[0], Values: []uint16{65535}}}, when)

	metadataOnly := device
	metadataOnly.RegisterBlocks = []RegisterBlock{{ID: 20, Name: "重命名", FunctionCode: 3, StartAddress: 0, Quantity: 1, SortOrder: 2}}
	store.Ensure(metadataOnly)
	state, _ := store.Get(device.ID)
	if state.RegisterBlocks[0].Name != "重命名" || state.RegisterBlocks[0].Values[0] == nil || *state.RegisterBlocks[0].Values[0] != 65535 {
		t.Fatalf("metadata update state = %#v, want retained snapshot", state.RegisterBlocks)
	}

	structural := metadataOnly
	structural.RegisterBlocks = []RegisterBlock{{ID: 20, Name: "重命名", FunctionCode: 4, StartAddress: 4, Quantity: 2, SortOrder: 2}}
	store.Ensure(structural)
	state, _ = store.Get(device.ID)
	if len(state.RegisterBlocks[0].Values) != 2 || state.RegisterBlocks[0].Values[0] != nil || state.RegisterBlocks[0].Valid {
		t.Fatalf("structural update state = %#v, want cleared snapshot", state.RegisterBlocks[0])
	}
}

func TestCurrentStateStoreInvalidatesSnapshotWhenDeviceAddressChanges(t *testing.T) {
	store := NewCurrentStateStore()
	device := Device{
		ID: 9, Name: "网络设备", ChannelID: 2, UnitID: 1,
		NetworkEndpoint: &NetworkEndpoint{Host: "192.0.2.10", Port: 502},
		RegisterBlocks:  []RegisterBlock{{ID: 30, Name: "原始", FunctionCode: 3, StartAddress: 0, Quantity: 1}},
	}
	at := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	store.Ensure(device)
	store.RecordCycle(device, []RegisterBlockRead{{Block: device.RegisterBlocks[0], Values: []uint16{1234}}}, at)

	updated := device
	updated.UnitID = 2
	updated.NetworkEndpoint = &NetworkEndpoint{Host: "192.0.2.11", Port: 502}
	store.Ensure(updated)
	state, ok := store.Get(device.ID)
	if !ok || state.UnitID != 2 || state.NetworkEndpoint == nil || state.NetworkEndpoint.Host != "192.0.2.11" {
		t.Fatalf("updated identity state = %#v, exists=%v", state, ok)
	}
	if state.Status != StatusInitial || state.RegisterBlocks[0].Valid || state.RegisterBlocks[0].Values[0] == nil || *state.RegisterBlocks[0].Values[0] != 1234 {
		t.Fatalf("invalidated state = %#v, want retained value marked invalid", state)
	}
	if state.LastSuccessAt == nil || !state.LastSuccessAt.Equal(at) {
		t.Fatalf("LastSuccessAt = %v, want retained timestamp %v", state.LastSuccessAt, at)
	}
}

func TestCurrentStateStoreRejectsStaleCycleAfterDeviceMoves(t *testing.T) {
	store := NewCurrentStateStore()
	channelA := Channel{ID: 30, Protocol: ProtocolModbusRTU}
	channelB := Channel{ID: 31, Protocol: ProtocolModbusRTU}
	block := RegisterBlock{ID: 301, Name: "原始块", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 1}
	oldDevice := Device{ID: 32, Name: "迁移设备", ChannelID: channelA.ID, UnitID: 1, Enabled: Enabled, RegisterBlocks: []RegisterBlock{block}}
	newDevice := oldDevice
	newDevice.ChannelID = channelB.ID

	store.ConfigureChannels([]Channel{channelA}, []Device{oldDevice})
	store.Ensure(oldDevice)
	store.RecordCycle(oldDevice, []RegisterBlockRead{{Block: block, Values: []uint16{100}}}, time.Now().UTC())
	store.ConfigureChannels([]Channel{channelB}, []Device{newDevice})
	store.Ensure(newDevice)
	store.RecordCycle(oldDevice, []RegisterBlockRead{{Block: block, Values: []uint16{999}}}, time.Now().UTC())

	state, ok := store.Get(oldDevice.ID)
	if !ok || state.ChannelID != channelB.ID || state.RegisterBlocks[0].Valid || state.RegisterBlocks[0].Values[0] == nil || *state.RegisterBlocks[0].Values[0] != 100 {
		t.Fatalf("stale cycle state = %#v, exists=%v, want new channel with invalidated old value", state, ok)
	}
}

func TestCurrentStateStoreRemovesDisabledDevice(t *testing.T) {
	store := NewCurrentStateStore()
	device := Device{ID: 8, Name: "设备 8"}
	store.Ensure(device)
	if _, ok := store.Get(device.ID); !ok {
		t.Fatal("state does not exist after Ensure")
	}

	store.Remove(device.ID)
	if _, ok := store.Get(device.ID); ok {
		t.Fatal("state still exists after Remove")
	}
}

func TestCurrentStateStoreNotifiesAfterCommittedSnapshot(t *testing.T) {
	store := NewCurrentStateStore()
	device := Device{ID: 181, ExternalID: "device-181", Name: "观察设备", ChannelID: 18, UnitID: 1, Enabled: Enabled, FailureThreshold: 1, RegisterBlocks: []RegisterBlock{{ID: 1811, FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 10, Quantity: 1}}}
	notified := make(chan CurrentState, 2)
	store.SetObserver(func(state CurrentState) { notified <- state })
	store.Ensure(device)
	initial := <-notified
	if initial.ExternalID != device.ExternalID || initial.RegisterBlocks[0].Values[0] != nil {
		t.Fatalf("initial observed state = %#v", initial)
	}
	value := uint16(42)
	store.RecordCycle(device, []RegisterBlockRead{{Block: device.RegisterBlocks[0], Values: []uint16{value}}}, time.Now().UTC())
	committed := <-notified
	if committed.Status != StatusOnline || committed.RegisterBlocks[0].Values[0] == nil || *committed.RegisterBlocks[0].Values[0] != value {
		t.Fatalf("committed observed state = %#v", committed)
	}
	committed.RegisterBlocks[0].Values[0] = nil
	current, ok := store.Get(device.ID)
	if !ok || current.RegisterBlocks[0].Values[0] == nil {
		t.Fatal("observer received a non-defensive state copy")
	}
}

func TestCurrentStateStoreAggregatesChannelRuntimeStatus(t *testing.T) {
	store := NewCurrentStateStore()
	channel := Channel{ID: 20, Name: "TCP 轮询组", Protocol: ProtocolModbusTCP}
	block := RegisterBlock{ID: 200, Name: "原始块", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 1}
	devices := []Device{
		{ID: 21, Name: "设备 21", ChannelID: 20, UnitID: 1, Enabled: Enabled, FailureThreshold: 1, RegisterBlocks: []RegisterBlock{block}},
		{ID: 22, Name: "设备 22", ChannelID: 20, UnitID: 1, Enabled: Enabled, FailureThreshold: 1, RegisterBlocks: []RegisterBlock{block}},
	}
	store.ConfigureChannels([]Channel{channel}, devices)
	assertChannelStatus(t, store, 20, ChannelStatusStarting)

	at := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	store.RecordCycle(devices[0], []RegisterBlockRead{{Block: block, Values: []uint16{1}}}, at)
	assertChannelStatus(t, store, 20, ChannelStatusDegraded)
	store.RecordCycle(devices[1], []RegisterBlockRead{{Block: block, Values: []uint16{2}}}, at.Add(time.Second))
	assertChannelStatus(t, store, 20, ChannelStatusOnline)

	store.RecordCycle(devices[0], []RegisterBlockRead{{Block: block, Err: errors.New("timeout")}}, at.Add(2*time.Second))
	store.RecordCycle(devices[1], []RegisterBlockRead{{Block: block, Err: errors.New("timeout")}}, at.Add(3*time.Second))
	state, ok := store.ChannelState(20)
	if !ok || state.Status != ChannelStatusOffline || state.LastAttemptAt == nil || !state.LastAttemptAt.Equal(at.Add(3*time.Second)) {
		t.Fatalf("channel state = %#v, exists=%v, want offline with latest attempt", state, ok)
	}

	store.ConfigureChannels([]Channel{{ID: 21, Name: "空通道", Protocol: ProtocolModbusUDP}}, nil)
	assertChannelStatus(t, store, 21, ChannelStatusIdle)
}

func TestCurrentStateStoreClearsChannelErrorAfterRecovery(t *testing.T) {
	store := NewCurrentStateStore()
	channel := Channel{ID: 40, Name: "恢复测试通道", Protocol: ProtocolModbusTCP}
	device := Device{
		ID: 41, Name: "恢复测试设备", ChannelID: channel.ID, UnitID: 1,
		Enabled: Enabled, FailureThreshold: 1,
		RegisterBlocks: []RegisterBlock{{ID: 401, Name: "原始块", FunctionCode: FunctionCodeReadHoldingRegisters, Quantity: 1}},
	}
	store.ConfigureChannels([]Channel{channel}, []Device{device})

	store.RecordCycle(device, []RegisterBlockRead{{Block: device.RegisterBlocks[0], Err: errors.New("endpoint timeout")}}, time.Now().UTC())
	failed, ok := store.ChannelState(channel.ID)
	if !ok || failed.Status != ChannelStatusOffline || failed.LastError != "endpoint timeout" {
		t.Fatalf("failed channel state = %#v, exists=%v, want offline with endpoint error", failed, ok)
	}

	store.RecordCycle(device, []RegisterBlockRead{{Block: device.RegisterBlocks[0], Values: []uint16{42}}}, time.Now().UTC())
	recovered, ok := store.ChannelState(channel.ID)
	if !ok || recovered.Status != ChannelStatusOnline || recovered.LastError != "" {
		t.Fatalf("recovered channel state = %#v, exists=%v, want online without stale error", recovered, ok)
	}
}

func assertChannelStatus(t *testing.T, store *CurrentStateStore, channelID int64, want ChannelRuntimeStatus) {
	t.Helper()
	state, ok := store.ChannelState(channelID)
	if !ok || state.Status != want {
		t.Fatalf("channel %d state = %#v, exists=%v, want %s", channelID, state, ok, want)
	}
}
