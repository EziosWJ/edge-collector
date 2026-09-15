package acquisition

import (
	"errors"
	"testing"
	"time"
)

func TestCurrentStateStoreKeepsFailedBlockValueAndMarksOffline(t *testing.T) {
	store := NewCurrentStateStore()
	device := Device{
		ID: 7, Name: "设备 7", ChannelID: 2, SlaveID: 3, FailureThreshold: 3,
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
