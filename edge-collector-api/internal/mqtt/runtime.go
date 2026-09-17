package mqtt

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"
)

type RuntimeState string

const (
	RuntimeStateDisabled     RuntimeState = "DISABLED"
	RuntimeStateConnecting   RuntimeState = "CONNECTING"
	RuntimeStateConnected    RuntimeState = "CONNECTED"
	RuntimeStateReconnecting RuntimeState = "RECONNECTING"
	RuntimeStateError        RuntimeState = "ERROR"
)

type RuntimeSnapshot struct {
	State              RuntimeState `json:"state"`
	Connected          bool         `json:"connected"`
	LastError          string       `json:"lastError,omitempty"`
	ReconnectAttempts  int          `json:"reconnectAttempts"`
	NextRetryAt        *time.Time   `json:"nextRetryAt,omitempty"`
	LastConnectedAt    *time.Time   `json:"lastConnectedAt,omitempty"`
	LastDisconnectedAt *time.Time   `json:"lastDisconnectedAt,omitempty"`
	SubscriptionFilter string       `json:"subscriptionFilter"`
}

type RuntimeCallbacks struct {
	OnConnected    func(context.Context, RuntimeConfig)
	OnDisconnected func(error)
	OnMessage      func(string, []byte)
}

type Runtime struct {
	repository *Repository
	factory    TransportFactory
	now        func() time.Time

	mu              sync.RWMutex
	state           RuntimeSnapshot
	config          RuntimeConfig
	transport       Transport
	transportCancel context.CancelFunc
	generation      uint64
	started         bool
	callbacks       RuntimeCallbacks
	refreshCh       chan struct{}
}

func NewRuntime(repository *Repository, factories ...TransportFactory) *Runtime {
	var factory TransportFactory
	if len(factories) > 0 {
		factory = factories[0]
	}
	if factory == nil {
		factory = NewPahoTransportFactory()
	}
	return &Runtime{
		repository: repository,
		factory:    factory,
		now:        func() time.Time { return time.Now().UTC() },
		state:      RuntimeSnapshot{State: RuntimeStateDisabled},
		refreshCh:  make(chan struct{}, 1),
	}
}

func (r *Runtime) SetCallbacks(callbacks RuntimeCallbacks) {
	r.mu.Lock()
	r.callbacks = callbacks
	r.mu.Unlock()
}

func (r *Runtime) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	r.mu.Lock()
	r.now = now
	r.mu.Unlock()
}

func (r *Runtime) Refresh(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.RLock()
	started := r.started
	r.mu.RUnlock()
	if !started {
		return nil
	}
	select {
	case <-r.refreshCh:
	default:
	}
	select {
	case r.refreshCh <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) Run(ctx context.Context) error {
	if r.repository == nil {
		return errors.New("MQTT repository is required")
	}
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return errors.New("MQTT runtime is already running")
	}
	r.started = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.started = false
		transport := r.transport
		r.transport = nil
		cancel := r.transportCancel
		r.transportCancel = nil
		r.state.Connected = false
		r.state.State = RuntimeStateDisabled
		r.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if transport != nil {
			_ = transport.Close()
		}
	}()

	r.applyConfig(ctx)
	retryTicker := time.NewTicker(time.Second)
	defer retryTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.refreshCh:
			r.applyConfig(ctx)
		case <-retryTicker.C:
			if r.shouldRetry() {
				r.applyConfig(ctx)
			}
		}
	}
}

func (r *Runtime) applyConfig(parent context.Context) {
	config, err := r.repository.RuntimeConfig(parent)
	if err != nil {
		r.setError(err)
		return
	}
	if err := ValidateConfig(config.Config); err != nil {
		r.setError(err)
		return
	}
	r.mu.RLock()
	oldConfig := r.config
	oldTransport := r.transport
	oldCancel := r.transportCancel
	oldState := r.state.State
	started := r.started
	r.mu.RUnlock()
	if !started {
		return
	}
	if config.Enabled == 0 {
		// Invalidate callbacks from the old client before closing it. Some MQTT
		// clients report a final disconnect asynchronously, and that callback
		// must not turn a committed DISABLED state back into RECONNECTING.
		r.mu.Lock()
		r.generation++
		r.config = config
		r.transport = nil
		r.transportCancel = nil
		r.state = RuntimeSnapshot{State: RuntimeStateDisabled}
		r.mu.Unlock()
		if oldCancel != nil {
			oldCancel()
		}
		if oldTransport != nil {
			_ = oldTransport.Close()
		}
		return
	}
	if oldTransport != nil && reflect.DeepEqual(oldConfig, config) && oldState != RuntimeStateError {
		return
	}
	if oldCancel != nil {
		oldCancel()
	}
	if oldTransport != nil {
		_ = oldTransport.Close()
	}
	r.mu.Lock()
	r.generation++
	generation := r.generation
	r.config = config
	r.transport = nil
	r.transportCancel = nil
	r.state.State = RuntimeStateConnecting
	r.state.Connected = false
	r.state.LastError = ""
	r.state.SubscriptionFilter = commandSubscription(config.Config)
	r.mu.Unlock()

	transportContext, cancel := context.WithCancel(parent)
	var transport Transport
	transport, err = r.factory(transportContext, config, TransportCallbacks{
		OnConnected:    func() { r.transportConnected(generation, nil) },
		OnDisconnected: func(err error) { r.transportDisconnected(generation, err) },
		OnMessage: func(topic string, payload []byte) {
			r.mu.RLock()
			callbacks := r.callbacks
			r.mu.RUnlock()
			if callbacks.OnMessage != nil {
				callbacks.OnMessage(topic, append([]byte(nil), payload...))
			}
		},
	})
	if err != nil {
		cancel()
		r.setError(err)
		return
	}
	r.mu.Lock()
	if r.generation != generation || !r.started {
		r.mu.Unlock()
		_ = transport.Close()
		cancel()
		return
	}
	r.transport = transport
	r.transportCancel = cancel
	r.mu.Unlock()
}

func commandSubscription(config Config) string {
	builder, err := NewTopicBuilder(config.TopicPrefix, config.EdgeID)
	if err != nil {
		return ""
	}
	return builder.CommandSubscription()
}

func (r *Runtime) transportConnected(generation uint64, transport Transport) {
	r.mu.RLock()
	if generation != r.generation || !r.started {
		r.mu.RUnlock()
		return
	}
	config := r.config
	callbacks := r.callbacks
	transportContext := context.Background()
	r.mu.RUnlock()
	go func() {
		if transport == nil {
			deadline := time.NewTimer(time.Duration(config.ConnectTimeoutMS) * time.Millisecond)
			for transport == nil {
				r.mu.RLock()
				if generation == r.generation {
					transport = r.transport
				}
				r.mu.RUnlock()
				if transport != nil {
					break
				}
				select {
				case <-deadline.C:
					return
				case <-time.After(5 * time.Millisecond):
				}
			}
			if !deadline.Stop() {
				select {
				case <-deadline.C:
				default:
				}
			}
		}
		ctx, cancel := context.WithTimeout(transportContext, time.Duration(config.ConnectTimeoutMS)*time.Millisecond)
		defer cancel()
		if err := transport.Subscribe(ctx, commandSubscription(config.Config), 1); err != nil {
			r.setError(err)
			return
		}
		now := r.clockNow()
		r.mu.Lock()
		if generation == r.generation {
			r.state.State = RuntimeStateConnected
			r.state.Connected = true
			r.state.LastError = ""
			r.state.LastConnectedAt = timePointer(now)
			r.state.NextRetryAt = nil
		}
		r.mu.Unlock()
		if callbacks.OnConnected != nil {
			callbacks.OnConnected(context.Background(), config)
		}
	}()
}

func (r *Runtime) transportDisconnected(generation uint64, err error) {
	now := r.clockNow()
	r.mu.Lock()
	if generation != r.generation || !r.started {
		r.mu.Unlock()
		return
	}
	r.state.State = RuntimeStateReconnecting
	r.state.Connected = false
	r.state.ReconnectAttempts++
	r.state.LastDisconnectedAt = timePointer(now)
	if err != nil {
		r.state.LastError = redactMQTTError(err.Error(), r.config.Password, r.config.ClientPrivateKey)
	}
	r.state.NextRetryAt = timePointer(now.Add(defaultBackoff(r.state.ReconnectAttempts, time.Duration(r.config.ReconnectMinMS)*time.Millisecond, time.Duration(r.config.ReconnectMaxMS)*time.Millisecond)))
	callbacks := r.callbacks
	r.mu.Unlock()
	if callbacks.OnDisconnected != nil {
		callbacks.OnDisconnected(err)
	}
}

func (r *Runtime) setError(err error) {
	now := r.clockNow()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.State = RuntimeStateError
	r.state.Connected = false
	if err != nil {
		r.state.LastError = redactMQTTError(err.Error(), r.config.Password, r.config.ClientPrivateKey)
	}
	r.state.ReconnectAttempts++
	r.state.NextRetryAt = timePointer(now.Add(defaultBackoff(r.state.ReconnectAttempts, time.Duration(maxInt(r.config.ReconnectMinMS, 1000))*time.Millisecond, time.Duration(maxInt(r.config.ReconnectMaxMS, 60000))*time.Millisecond)))
}

func (r *Runtime) shouldRetry() bool {
	now := r.clockNow()
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.started && r.config.Enabled == 1 && r.state.State == RuntimeStateError && (r.state.NextRetryAt == nil || !now.Before(*r.state.NextRetryAt))
}

func (r *Runtime) clockNow() time.Time {
	r.mu.RLock()
	now := r.now
	r.mu.RUnlock()
	if now == nil {
		return time.Now().UTC()
	}
	return now().UTC()
}

func (r *Runtime) State() RuntimeSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value := r.state
	value.NextRetryAt = timePointerValue(value.NextRetryAt)
	value.LastConnectedAt = timePointerValue(value.LastConnectedAt)
	value.LastDisconnectedAt = timePointerValue(value.LastDisconnectedAt)
	return value
}

func (r *Runtime) Publish(ctx context.Context, publication Publication) error {
	r.mu.RLock()
	transport := r.transport
	connected := r.state.Connected
	password, privateKey := r.config.Password, r.config.ClientPrivateKey
	r.mu.RUnlock()
	if transport == nil || !connected {
		return ErrMQTTNotConnected
	}
	return redactMQTTErrorValue(transport.Publish(ctx, publication), password, privateKey)
}

func (r *Runtime) TestConnection(ctx context.Context, config RuntimeConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateConfig(config.Config); err != nil {
		return err
	}
	if config.Enabled == 0 {
		config.Enabled = 1
	}
	transport, err := r.factory(ctx, config, TransportCallbacks{})
	if err != nil {
		return err
	}
	defer transport.Close()
	if waiter, ok := transport.(ConnectedWaiter); ok {
		return waiter.WaitConnected(ctx)
	}
	return nil
}

// redactMQTTErrorValue keeps errors.Is useful to callers while ensuring that
// connector diagnostics cannot expose a configured password or private key
// through runtime state, outbox health, or application logs.
func redactMQTTErrorValue(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	message := redactMQTTError(err.Error(), secrets...)
	if message == err.Error() {
		return err
	}
	return redactedMQTTError{cause: err, message: message}
}

type redactedMQTTError struct {
	cause   error
	message string
}

func (e redactedMQTTError) Error() string { return e.message }

func (e redactedMQTTError) Unwrap() error { return e.cause }

func redactMQTTError(message string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return message
}

func timePointer(value time.Time) *time.Time { return &value }

func timePointerValue(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
