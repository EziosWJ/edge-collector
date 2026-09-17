package mqtt

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition/script"
)

var ErrOutboxIngressFull = errors.New("MQTT_OUTBOX_INGRESS_FULL")

type OutboxConnectionState interface {
	State() RuntimeSnapshot
}

// ReliableOutboxWorker persists messages submitted by projections through a
// bounded ingress and drains durable rows only while the MQTT runtime is
// connected. The durable row, rather than the in-memory ingress, is the
// restart recovery boundary.
type ReliableOutboxWorker struct {
	repository *Repository
	publisher  Publisher
	logger     *log.Logger
	ingress    chan OutboxMessage
	wake       chan struct{}

	mu             sync.RWMutex
	connected      func() bool
	publishTimeout time.Duration
	lastError      error
}

func NewReliableOutboxWorker(repository *Repository, publisher Publisher, ingressCapacity int, logger *log.Logger) *ReliableOutboxWorker {
	if ingressCapacity < 1 {
		ingressCapacity = 256
	}
	if logger == nil {
		logger = log.Default()
	}
	worker := &ReliableOutboxWorker{
		repository:     repository,
		publisher:      publisher,
		logger:         logger,
		ingress:        make(chan OutboxMessage, ingressCapacity),
		wake:           make(chan struct{}, 1),
		publishTimeout: 5 * time.Second,
	}
	worker.connected = func() bool {
		if stateReader, ok := publisher.(OutboxConnectionState); ok {
			return stateReader.State().State == RuntimeStateConnected
		}
		return true
	}
	return worker
}

func (w *ReliableOutboxWorker) SetConnected(connected func() bool) {
	if connected == nil {
		return
	}
	w.mu.Lock()
	w.connected = connected
	w.mu.Unlock()
}

func (w *ReliableOutboxWorker) SetPublishTimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	w.mu.Lock()
	w.publishTimeout = timeout
	w.mu.Unlock()
}

// Submit never waits for the broker or the database. A full ingress is an
// explicit bounded runtime error; it is not silently converted into an
// unbounded goroutine or an outbox eviction.
func (w *ReliableOutboxWorker) Submit(message OutboxMessage) error {
	if err := validateOutboxMessage(message); err != nil {
		return err
	}
	select {
	case w.ingress <- message:
		w.signal()
		return nil
	default:
		return ErrOutboxIngressFull
	}
}

func (w *ReliableOutboxWorker) IngressLen() int { return len(w.ingress) }

func (w *ReliableOutboxWorker) LastError() error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastError
}

func (w *ReliableOutboxWorker) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if w.repository == nil || w.publisher == nil {
		return errors.New("reliable MQTT outbox dependencies are required")
	}
	nextRetry := time.Time{}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		persisted := w.persistOne(ctx)
		if w.isConnected() && !time.Now().Before(nextRetry) {
			drained, err := w.drainOne(ctx)
			if err != nil {
				nextRetry = time.Now().Add(time.Second)
				w.reportError(err)
			} else if drained {
				nextRetry = time.Time{}
			}
			if drained && err == nil {
				continue
			}
		}
		wait := time.Second
		if w.isConnected() {
			wait = 250 * time.Millisecond
		}
		if !nextRetry.IsZero() && nextRetry.After(time.Now()) && nextRetry.Sub(time.Now()) < wait {
			wait = nextRetry.Sub(time.Now())
		}
		if persisted {
			wait = 0
		}
		if wait <= 0 {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-w.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}

func (w *ReliableOutboxWorker) persistOne(ctx context.Context) bool {
	select {
	case message := <-w.ingress:
		if err := w.repository.Enqueue(ctx, message); err != nil {
			w.reportError(err)
		}
		return true
	default:
		return false
	}
}

func (w *ReliableOutboxWorker) drainOne(parent context.Context) (bool, error) {
	message, err := w.repository.NextOutbox(parent, time.Now().UTC())
	if err != nil || message == nil {
		return message != nil, err
	}
	w.mu.RLock()
	timeout := w.publishTimeout
	w.mu.RUnlock()
	ctx, cancel := context.WithTimeout(parent, timeout)
	err = w.publisher.Publish(ctx, Publication{Topic: message.Topic, QoS: byte(message.QoS), Retain: message.Retain == 1, Payload: []byte(message.Payload)})
	cancel()
	if err != nil {
		markErr := w.repository.MarkOutboxAttempt(parent, message.ID, time.Now().UTC(), err)
		if markErr != nil {
			return true, errors.Join(err, markErr)
		}
		return true, err
	}
	if err := w.repository.DeleteOutbox(parent, message.ID); err != nil {
		return true, err
	}
	return true, nil
}

func (w *ReliableOutboxWorker) isConnected() bool {
	w.mu.RLock()
	connected := w.connected
	w.mu.RUnlock()
	return connected != nil && connected()
}

func (w *ReliableOutboxWorker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *ReliableOutboxWorker) reportError(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	w.lastError = err
	w.mu.Unlock()
	// Do not include payloads or credentials in this diagnostic.
	w.logger.Printf("mqtt_outbox_worker error_type=%T error=%v", err, err)
}

// EventProjector converts committed generic Starlark events into reliable
// device-event/v1 outbox rows. It intentionally does not call the publisher.
type EventProjector struct {
	worker *ReliableOutboxWorker

	mu        sync.RWMutex
	builder   TopicBuilder
	retention time.Duration
	now       func() time.Time
}

func NewEventProjector(worker *ReliableOutboxWorker, builder TopicBuilder, retention time.Duration) *EventProjector {
	if retention <= 0 {
		retention = time.Duration(DefaultOutboxRetentionDays) * 24 * time.Hour
	}
	return &EventProjector{worker: worker, builder: builder, retention: retention, now: func() time.Time { return time.Now().UTC() }}
}

func (p *EventProjector) Configure(builder TopicBuilder, retention time.Duration) {
	if retention <= 0 {
		retention = time.Duration(DefaultOutboxRetentionDays) * 24 * time.Hour
	}
	p.mu.Lock()
	p.builder = builder
	p.retention = retention
	p.mu.Unlock()
}

func (p *EventProjector) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	p.mu.Lock()
	p.now = now
	p.mu.Unlock()
}

func (p *EventProjector) Emit(ctx context.Context, device acquisition.Device, version acquisition.ScriptVersion, events []script.Event) {
	if p.worker == nil || device.ExternalID == "" {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		return
	}
	p.mu.RLock()
	builder := p.builder
	retention := p.retention
	nowFn := p.now
	p.mu.RUnlock()
	now := time.Now().UTC()
	if nowFn != nil {
		now = nowFn().UTC()
	}
	topic, err := builder.DeviceEvent(device.ExternalID)
	if err != nil {
		return
	}
	for _, event := range events {
		messageID := NewMessageID(now)
		payload, err := BuildDeviceEvent(builder.EdgeID(), device.ExternalID, messageID, event, script.ScriptVersion{ScriptID: version.ScriptID, VersionID: version.ID, VersionNo: version.VersionNo, Source: version.Source, Checksum: version.Checksum}, now)
		if err != nil {
			p.worker.reportError(err)
			continue
		}
		expiresAt := now.Add(retention)
		message := OutboxMessage{MessageID: messageID, MessageType: OutboxMessageTypeEvent, Topic: topic, QoS: 1, Retain: 0, Payload: string(payload), PayloadBytes: int64(len(payload)), Priority: OutboxPriorityReliableEvent, CreatedAt: now, ExpiresAt: &expiresAt}
		if err := p.worker.Submit(message); err != nil {
			p.worker.reportError(err)
		}
	}
}
