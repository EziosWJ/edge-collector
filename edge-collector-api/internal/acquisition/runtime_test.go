package acquisition

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
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
		{ID: 1, Name: "设备 1", ChannelID: 9, SlaveID: 1, DeviceType: DeviceTypeFeedProtector, Enabled: Enabled, FailureThreshold: 3},
		{ID: 2, Name: "设备 2", ChannelID: 9, SlaveID: 2, DeviceType: DeviceTypeFeedProtector, Enabled: Enabled, FailureThreshold: 3},
		{ID: 3, Name: "设备 3", ChannelID: 9, SlaveID: 3, DeviceType: DeviceTypeFeedProtector, Enabled: Enabled, FailureThreshold: 3},
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

func TestRuntimeRefreshRemovesDisabledDeviceState(t *testing.T) {
	store := NewCurrentStateStore()
	session := &fakeModbusSession{
		registers: map[uint8][]uint16{
			1: {2200, 100, 0, 1000, 5000, 980, 1},
		},
	}
	channel := Channel{ID: 9, Enabled: Enabled}
	device := Device{ID: 1, Name: "设备 1", ChannelID: 9, SlaveID: 1, DeviceType: DeviceTypeFeedProtector, Enabled: Enabled, PollIntervalMS: 60000, FailureThreshold: 3}

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
	runtime, err := NewRuntime([]Channel{channel}, []Device{device}, store, func(Channel) (ModbusSession, error) {
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

type fakeModbusSession struct {
	mu        sync.Mutex
	current   uint8
	called    []uint8
	registers map[uint8][]uint16
	errors    map[uint8]error
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
	s.mu.Lock()
	id := s.current
	s.mu.Unlock()
	if err := s.errors[id]; err != nil {
		return nil, err
	}
	registers := s.registers[id]
	if address == 6 && quantity == 1 {
		return []uint16{registers[6]}, nil
	}
	return registers[:6], nil
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
