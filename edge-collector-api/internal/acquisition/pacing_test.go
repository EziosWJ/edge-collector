package acquisition

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPacedSessionAddsDelayBetweenRequestsIncludingFailures(t *testing.T) {
	underlying := &pacingSessionFake{errors: map[uint16]error{12: errors.New("timeout")}}
	session := newPacedSession(underlying, 20*time.Millisecond)
	clock := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	var waits []time.Duration
	session.now = func() time.Time { return clock }
	session.sleep = func(_ context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		clock = clock.Add(duration)
		return nil
	}

	if _, err := session.ReadHoldingRegisters(context.Background(), 1, 0, 6); err != nil {
		t.Fatalf("first request error = %v", err)
	}
	if _, err := session.ReadHoldingRegisters(context.Background(), 1, 6, 1); err != nil {
		t.Fatalf("second request error = %v", err)
	}
	if _, err := session.ReadHoldingRegisters(context.Background(), 1, 12, 1); err == nil {
		t.Fatal("failed request error = nil")
	}
	if _, err := session.ReadHoldingRegisters(context.Background(), 2, 0, 6); err != nil {
		t.Fatalf("request after failure error = %v", err)
	}

	if len(waits) != 3 {
		t.Fatalf("business waits = %v, want three waits", waits)
	}
	for i, wait := range waits {
		if wait != 20*time.Millisecond {
			t.Errorf("wait %d = %v, want 20ms", i, wait)
		}
	}
	if len(underlying.requests) != 4 {
		t.Fatalf("underlying requests = %d, want 4", len(underlying.requests))
	}
}

func TestPacedSessionZeroDelayDoesNotSleep(t *testing.T) {
	underlying := &pacingSessionFake{}
	session := newPacedSession(underlying, 0)
	slept := false
	session.sleep = func(context.Context, time.Duration) error {
		slept = true
		return nil
	}

	for i := 0; i < 2; i++ {
		if _, err := session.ReadHoldingRegisters(context.Background(), 1, uint16(i), 1); err != nil {
			t.Fatalf("request %d error = %v", i, err)
		}
	}
	if slept {
		t.Fatal("zero-delay session performed a business sleep")
	}
}

func TestNextDeviceOrdersByDueTimeThenID(t *testing.T) {
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	devices := []Device{{ID: 3}, {ID: 1}, {ID: 2}}
	nextDue := map[int64]time.Time{
		1: now.Add(time.Second),
		2: now.Add(time.Second),
		3: now.Add(500 * time.Millisecond),
	}

	device, due, waitFor := nextDevice(devices, nextDue, now)
	if due || device.ID != 0 || waitFor != 500*time.Millisecond {
		t.Fatalf("next device before due = %#v, due=%v, wait=%v", device, due, waitFor)
	}

	device, due, _ = nextDevice(devices, nextDue, now.Add(2*time.Second))
	if !due || device.ID != 3 {
		t.Fatalf("next due device = %#v, due=%v, want device 3", device, due)
	}

	nextDue[3] = now.Add(3 * time.Second)
	device, due, _ = nextDevice(devices, nextDue, now.Add(2*time.Second))
	if !due || device.ID != 1 {
		t.Fatalf("tie due device = %#v, due=%v, want device 1", device, due)
	}
}

func TestActiveChannelConfigFiltersDisabledDevices(t *testing.T) {
	configs := activeChannelConfigs(
		[]Channel{{ID: 9, Enabled: Enabled}},
		[]Device{{ID: 2, ChannelID: 9, Enabled: Disabled}, {ID: 1, ChannelID: 9, Enabled: Enabled, RegisterBlocks: testBlocks(31)}},
	)
	config, ok := configs[9]
	if !ok || len(config.devices) != 1 || config.devices[0].ID != 1 {
		t.Fatalf("active channel config = %#v, want only enabled device 1", configs)
	}
}

func TestSamePhysicalChannelIgnoresPacingAndScheduleChanges(t *testing.T) {
	left := Channel{Port: "/dev/ttyUSB0", BaudRate: 19200, DataBits: 8, StopBits: 1, Parity: "N", TimeoutMS: 300, InterRequestDelayMS: 0}
	right := left
	right.InterRequestDelayMS = 100
	if !samePhysicalChannel(left, right) {
		t.Fatal("pacing-only change unexpectedly requires a new physical session")
	}
	right.Port = "/dev/ttyUSB1"
	if samePhysicalChannel(left, right) {
		t.Fatal("port change did not require a new physical session")
	}
}

type pacingRequest struct {
	address  uint16
	quantity uint16
}

type pacingSessionFake struct {
	requests []pacingRequest
	errors   map[uint16]error
}

func (f *pacingSessionFake) Open() error           { return nil }
func (f *pacingSessionFake) Close() error          { return nil }
func (f *pacingSessionFake) SetUnitID(uint8) error { return nil }

func (f *pacingSessionFake) ReadHoldingRegisters(_ context.Context, _ uint8, address, quantity uint16) ([]uint16, error) {
	f.requests = append(f.requests, pacingRequest{address: address, quantity: quantity})
	if err := f.errors[address]; err != nil {
		return nil, err
	}
	return []uint16{1, 2, 3, 4, 5, 6}, nil
}

func (f *pacingSessionFake) ReadInputRegisters(ctx context.Context, slaveID uint8, address, quantity uint16) ([]uint16, error) {
	return f.ReadHoldingRegisters(ctx, slaveID, address, quantity)
}
