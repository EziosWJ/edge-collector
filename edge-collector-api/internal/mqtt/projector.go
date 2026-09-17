package mqtt

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
)

// Publisher is the non-blocking boundary between MQTT projections and the
// connected client. Runtime.Publish implements it and waits for QoS1 PUBACK;
// the projector itself never owns an MQTT connection.
type Publisher interface {
	Publish(context.Context, Publication) error
}

type projectionEntry struct {
	state       acquisition.CurrentState
	generation  uint64
	statusDue   bool
	rawDue      bool
	rawReady    bool
	forceRaw    bool
	lastRawAt   time.Time
	statusRetry time.Time
	rawRetry    time.Time
	statusTry   int
	rawTry      int
}

type projectionTask struct {
	deviceID   string
	generation uint64
	state      acquisition.CurrentState
	builder    TopicBuilder
	kind       string
}

// LatestStateProjector turns committed CurrentState snapshots into one
// bounded latest entry per device. It uses one worker for all devices; no
// acquisition callback performs broker I/O and raw snapshots never enter the
// reliable outbox.
type LatestStateProjector struct {
	publisher Publisher

	mu             sync.Mutex
	builder        TopicBuilder
	interval       time.Duration
	publishTimeout time.Duration
	now            func() time.Time
	entries        map[string]projectionEntry
	wake           chan struct{}
}

func NewLatestStateProjector(publisher Publisher, builder TopicBuilder, interval time.Duration) *LatestStateProjector {
	if interval <= 0 {
		interval = time.Duration(DefaultRawPublishIntervalMS) * time.Millisecond
	}
	return &LatestStateProjector{
		publisher:      publisher,
		builder:        builder,
		interval:       interval,
		publishTimeout: 2 * time.Second,
		now:            func() time.Time { return time.Now().UTC() },
		entries:        make(map[string]projectionEntry),
		wake:           make(chan struct{}, 1),
	}
}

func (p *LatestStateProjector) SetPublishTimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	p.mu.Lock()
	p.publishTimeout = timeout
	p.mu.Unlock()
}

func (p *LatestStateProjector) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	p.mu.Lock()
	p.now = now
	p.mu.Unlock()
}

func (p *LatestStateProjector) Configure(builder TopicBuilder, interval time.Duration) {
	if interval <= 0 {
		interval = time.Duration(DefaultRawPublishIntervalMS) * time.Millisecond
	}
	p.mu.Lock()
	p.builder = builder
	p.interval = interval
	p.mu.Unlock()
	p.signal()
}

// OnState is suitable for CurrentStateStore.SetObserver. It updates status
// immediately after each committed communication-state change. Raw is marked
// only by OnCycle, because a static read is not the complete acquisition
// cycle when after_poll is configured.
func (p *LatestStateProjector) OnState(state acquisition.CurrentState) {
	p.upsert(state, false)
}

// OnCycle is suitable for acquisition.RuntimeScriptConfig.CycleSink. It is
// called after static reads and after_poll have completed, so the raw latest
// snapshot cannot be observed halfway through a device cycle.
func (p *LatestStateProjector) OnCycle(state acquisition.CurrentState) {
	p.upsert(state, true)
}

func (p *LatestStateProjector) upsert(state acquisition.CurrentState, cycleComplete bool) {
	if state.ExternalID == "" {
		return
	}
	state = cloneCurrentState(state)
	p.mu.Lock()
	entry, exists := p.entries[state.ExternalID]
	entry.state = state
	entry.generation++
	if !entry.statusDue {
		entry.statusRetry = time.Time{}
		entry.statusTry = 0
	}
	if !entry.rawDue {
		entry.rawRetry = time.Time{}
		entry.rawTry = 0
	}
	entry.statusDue = true
	if cycleComplete {
		entry.rawDue = true
		entry.rawReady = true
	}
	if !exists {
		entry.lastRawAt = time.Time{}
	}
	p.entries[state.ExternalID] = entry
	p.mu.Unlock()
	p.signal()
}

func (p *LatestStateProjector) Seed(states []acquisition.CurrentState) {
	for _, state := range states {
		p.OnState(state)
	}
}

// Republish marks the latest state for every known device for a reconnect.
// Status is retried immediately; raw ignores the previous interval once so a
// reconnect cannot wait for the next poll to restore the latest snapshot.
func (p *LatestStateProjector) Republish() {
	p.mu.Lock()
	for deviceID, entry := range p.entries {
		entry.statusDue = true
		entry.statusRetry = time.Time{}
		entry.statusTry = 0
		if entry.rawReady {
			entry.rawDue = true
			entry.forceRaw = true
		}
		entry.rawRetry = time.Time{}
		entry.rawTry = 0
		p.entries[deviceID] = entry
	}
	p.mu.Unlock()
	p.signal()
}

func (p *LatestStateProjector) PendingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, entry := range p.entries {
		if entry.statusDue || entry.rawDue {
			count++
		}
	}
	return count
}

func (p *LatestStateProjector) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *LatestStateProjector) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		task, wait := p.nextTask(p.clockNow())
		if task != nil {
			p.publish(ctx, *task)
			continue
		}
		if wait <= 0 {
			wait = time.Second
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
		case <-p.wake:
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

func (p *LatestStateProjector) nextTask(now time.Time) (*projectionTask, time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	deviceIDs := make([]string, 0, len(p.entries))
	for deviceID := range p.entries {
		deviceIDs = append(deviceIDs, deviceID)
	}
	sort.Strings(deviceIDs)
	var next time.Time
	for _, deviceID := range deviceIDs {
		entry := p.entries[deviceID]
		if entry.statusDue && (entry.statusRetry.IsZero() || !now.Before(entry.statusRetry)) {
			entry.statusTry++
			entry.statusRetry = now.Add(retryDelay(entry.statusTry))
			p.entries[deviceID] = entry
			return &projectionTask{deviceID: deviceID, generation: entry.generation, state: cloneCurrentState(entry.state), builder: p.builder, kind: "status"}, 0
		}
		if entry.rawDue {
			rawDueAt := entry.lastRawAt.Add(p.interval)
			if entry.forceRaw || entry.lastRawAt.IsZero() {
				rawDueAt = now
			}
			if !now.Before(rawDueAt) && (entry.rawRetry.IsZero() || !now.Before(entry.rawRetry)) {
				entry.rawTry++
				entry.rawRetry = now.Add(retryDelay(entry.rawTry))
				p.entries[deviceID] = entry
				return &projectionTask{deviceID: deviceID, generation: entry.generation, state: cloneCurrentState(entry.state), builder: p.builder, kind: "raw"}, 0
			}
			candidate := rawDueAt
			if !entry.rawRetry.IsZero() && candidate.Before(entry.rawRetry) {
				candidate = entry.rawRetry
			}
			next = earlierTime(next, candidate)
		}
		if entry.statusDue && !entry.statusRetry.IsZero() {
			next = earlierTime(next, entry.statusRetry)
		}
	}
	if next.IsZero() {
		return nil, time.Hour
	}
	if !next.After(now) {
		return nil, time.Millisecond
	}
	return nil, next.Sub(now)
}

func (p *LatestStateProjector) publish(parent context.Context, task projectionTask) {
	p.mu.Lock()
	timeout := p.publishTimeout
	now := p.clockNowLocked()
	p.mu.Unlock()
	payload, publication, err := p.buildPublication(task, now)
	if err == nil {
		ctx, cancel := context.WithTimeout(parent, timeout)
		err = p.publisher.Publish(ctx, Publication{Topic: publication.Topic, QoS: publication.QoS, Retain: publication.Retain, Payload: payload})
		cancel()
	}
	p.finish(task, err, now)
}

func (p *LatestStateProjector) buildPublication(task projectionTask, at time.Time) ([]byte, Publication, error) {
	if p.publisher == nil {
		return nil, Publication{}, ErrMQTTNotConnected
	}
	messageID := NewMessageID(at)
	if task.kind == "status" {
		topic, err := task.builder.DeviceStatus(task.deviceID)
		if err != nil {
			return nil, Publication{}, err
		}
		payload, err := BuildDeviceStatus(task.builder.EdgeID(), task.deviceID, messageID, task.state, at)
		return payload, Publication{Topic: topic, QoS: 1, Retain: true}, err
	}
	topic, err := task.builder.DeviceRaw(task.deviceID)
	if err != nil {
		return nil, Publication{}, err
	}
	payload, err := BuildRawSnapshot(task.builder.EdgeID(), task.deviceID, messageID, task.state, at)
	return payload, Publication{Topic: topic, QoS: 0, Retain: false}, err
}

func (p *LatestStateProjector) finish(task projectionTask, err error, at time.Time) {
	p.mu.Lock()
	entry, ok := p.entries[task.deviceID]
	if ok && entry.generation == task.generation {
		if task.kind == "status" {
			if err == nil {
				entry.statusDue = false
				entry.statusRetry = time.Time{}
				entry.statusTry = 0
			}
		} else if err == nil {
			entry.rawDue = false
			entry.forceRaw = false
			entry.rawRetry = time.Time{}
			entry.rawTry = 0
			entry.lastRawAt = at
		}
		p.entries[task.deviceID] = entry
	}
	p.mu.Unlock()
}

func (p *LatestStateProjector) clockNow() time.Time {
	p.mu.Lock()
	now := p.now
	p.mu.Unlock()
	if now == nil {
		return time.Now().UTC()
	}
	return now().UTC()
}

func (p *LatestStateProjector) clockNowLocked() time.Time {
	if p.now == nil {
		return time.Now().UTC()
	}
	return p.now().UTC()
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return defaultBackoff(attempt-1, time.Second, 30*time.Second)
}

func earlierTime(current, candidate time.Time) time.Time {
	if candidate.IsZero() || (!current.IsZero() && !candidate.Before(current)) {
		return current
	}
	return candidate
}

func cloneCurrentState(value acquisition.CurrentState) acquisition.CurrentState {
	value.NetworkEndpoint = cloneNetworkEndpoint(value.NetworkEndpoint)
	value.RegisterBlocks = append([]acquisition.RegisterBlockState(nil), value.RegisterBlocks...)
	for index := range value.RegisterBlocks {
		value.RegisterBlocks[index].Values = append([]*uint16(nil), value.RegisterBlocks[index].Values...)
		for valueIndex, register := range value.RegisterBlocks[index].Values {
			if register == nil {
				continue
			}
			copy := *register
			value.RegisterBlocks[index].Values[valueIndex] = &copy
		}
		value.RegisterBlocks[index].LastAttemptAt = cloneTime(value.RegisterBlocks[index].LastAttemptAt)
		value.RegisterBlocks[index].LastSuccessAt = cloneTime(value.RegisterBlocks[index].LastSuccessAt)
	}
	value.LastAttemptAt = cloneTime(value.LastAttemptAt)
	value.LastSuccessAt = cloneTime(value.LastSuccessAt)
	return value
}

func cloneNetworkEndpoint(value *acquisition.NetworkEndpoint) *acquisition.NetworkEndpoint {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
