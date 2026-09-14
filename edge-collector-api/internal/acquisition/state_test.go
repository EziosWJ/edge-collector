package acquisition

import (
	"testing"
	"time"
)

func TestCurrentStateStoreKeepsPartialValuesAndMarksOffline(t *testing.T) {
	store := NewCurrentStateStore()
	device := Device{ID: 7, Name: "馈电保护器 7", ChannelID: 2, SlaveID: 3, FailureThreshold: 3}
	firstAt := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	first := FeedProtectorReading{
		Data: FeedProtectorData{Voltage: 220, Current: 8.5},
		ValidFields: map[string]bool{
			"voltage": true,
			"current": true,
		},
	}

	store.Record(device, first, nil, firstAt)
	state, ok := store.Get(device.ID)
	if !ok || state.Status != StatusOnline {
		t.Fatalf("initial state = %#v, exists=%v, want online", state, ok)
	}

	partialAt := firstAt.Add(time.Second)
	partial := FeedProtectorReading{
		Data:        FeedProtectorData{Voltage: 221},
		ValidFields: map[string]bool{"voltage": true},
	}
	store.Record(device, partial, errPartialRead, partialAt)
	state, _ = store.Get(device.ID)
	if state.Data.Voltage != 221 || state.Data.Current != 8.5 {
		t.Fatalf("partial data = %#v, want voltage updated and current retained", state.Data)
	}
	if state.FieldValidity["current"] {
		t.Fatal("current field validity = true, want false after partial read")
	}
	if state.FieldUpdatedAt["current"] != firstAt {
		t.Errorf("current field updated at = %v, want %v", state.FieldUpdatedAt["current"], firstAt)
	}
	if state.Status != StatusDegraded || state.ConsecutiveFailures != 1 {
		t.Errorf("partial state = %#v, want degraded with one failure", state)
	}

	for i := 0; i < 2; i++ {
		store.Record(device, FeedProtectorReading{}, errPartialRead, partialAt.Add(time.Duration(i+2)*time.Second))
	}
	state, _ = store.Get(device.ID)
	if state.Status != StatusOffline || state.ConsecutiveFailures != 3 {
		t.Errorf("offline state = %#v, want offline after three failures", state)
	}

	recoveredAt := partialAt.Add(4 * time.Second)
	store.Record(device, first, nil, recoveredAt)
	state, _ = store.Get(device.ID)
	if state.Status != StatusOnline || state.ConsecutiveFailures != 0 {
		t.Errorf("recovered state = %#v, want online with zero failures", state)
	}
	if state.LastSuccessAt == nil || !state.LastSuccessAt.Equal(recoveredAt) {
		t.Errorf("LastSuccessAt = %v, want %v", state.LastSuccessAt, recoveredAt)
	}
}

func TestCurrentStateStoreRemovesDisabledDevice(t *testing.T) {
	store := NewCurrentStateStore()
	device := Device{ID: 8, Name: "馈电保护器 8", ChannelID: 2, SlaveID: 4}
	store.Ensure(device)
	if _, ok := store.Get(device.ID); !ok {
		t.Fatal("state does not exist after Ensure")
	}

	store.Remove(device.ID)
	if _, ok := store.Get(device.ID); ok {
		t.Fatal("state still exists after Remove")
	}
}
