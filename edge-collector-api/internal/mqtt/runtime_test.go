package mqtt

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
)

func TestRuntimeRestoresSubscriptionAndHonorsDisabledConfig(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	current, err := repository.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	input := configInput(current)
	input.Enabled = true
	if _, err := repository.SaveConfig(context.Background(), input, auditEventForTest()); err != nil {
		t.Fatal(err)
	}
	fake := &testTransport{}
	factory := func(_ context.Context, _ RuntimeConfig, callbacks TransportCallbacks) (Transport, error) {
		fake.mu.Lock()
		fake.callbacks = callbacks
		fake.mu.Unlock()
		return fake, nil
	}
	runtime := NewRuntime(repository, factory)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	waitFor(t, func() bool { return runtime.State().State == RuntimeStateConnecting })
	fake.mu.Lock()
	callbacks := fake.callbacks
	fake.mu.Unlock()
	callbacks.OnConnected()
	waitFor(t, func() bool { return runtime.State().State == RuntimeStateConnected })
	fake.mu.Lock()
	filters := append([]string(nil), fake.subscriptions...)
	fake.mu.Unlock()
	if len(filters) != 1 || filters[0] != "edge/edge-01/device/+/command" {
		t.Fatalf("subscriptions = %v", filters)
	}
	current, err = repository.GetConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input = configInput(current)
	input.Enabled = false
	fake.closeCallsDisconnected = true
	if _, err := repository.SaveConfig(ctx, input, auditEventForTest()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return runtime.State().State == RuntimeStateDisabled })
	time.Sleep(50 * time.Millisecond)
	if state := runtime.State(); state.State != RuntimeStateDisabled || state.Connected {
		t.Fatalf("runtime state after stale disconnect callback = %#v", state)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not stop")
	}
}

func TestRuntimeRebuildsTransportAfterSubscriptionError(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	current, err := repository.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	input := configInput(current)
	input.Enabled = true
	input.ReconnectMinMS = 100
	input.ReconnectMaxMS = 100
	if _, err := repository.SaveConfig(context.Background(), input, auditEventForTest()); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	factoryCalls := 0
	fake := &testTransport{subscribeErr: errors.New("subscription unavailable")}
	factory := func(_ context.Context, _ RuntimeConfig, callbacks TransportCallbacks) (Transport, error) {
		mu.Lock()
		factoryCalls++
		mu.Unlock()
		fake.mu.Lock()
		fake.callbacks = callbacks
		fake.mu.Unlock()
		return fake, nil
	}
	runtime := NewRuntime(repository, factory)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	waitFor(t, func() bool { return runtime.State().State == RuntimeStateConnecting })
	fake.mu.Lock()
	callbacks := fake.callbacks
	fake.mu.Unlock()
	callbacks.OnConnected()
	waitFor(t, func() bool { return runtime.State().State == RuntimeStateError })
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		calls := factoryCalls
		mu.Unlock()
		if calls >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	calls := factoryCalls
	mu.Unlock()
	if calls < 2 {
		t.Fatalf("factory calls = %d, want a retry", calls)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not stop")
	}
}

type testTransport struct {
	mu                     sync.Mutex
	callbacks              TransportCallbacks
	subscriptions          []string
	subscribeErr           error
	closed                 bool
	closeCallsDisconnected bool
}

func (t *testTransport) Publish(context.Context, Publication) error { return nil }

func (t *testTransport) Subscribe(_ context.Context, topic string, _ byte) error {
	t.mu.Lock()
	t.subscriptions = append(t.subscriptions, topic)
	err := t.subscribeErr
	t.mu.Unlock()
	return err
}

func (t *testTransport) Close() error {
	t.mu.Lock()
	t.closed = true
	callback := t.callbacks.OnDisconnected
	callCallback := t.closeCallsDisconnected
	t.mu.Unlock()
	if callCallback && callback != nil {
		callback(nil)
	}
	return nil
}

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not reached")
}

func auditEventForTest() audit.Event {
	return audit.Event{Action: "mqtt.test", Resource: "MQTT 配置"}
}
