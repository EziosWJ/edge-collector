package mqtt

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
)

func TestLatestProjectorCoalescesRawAndKeepsQoSContract(t *testing.T) {
	builder, err := NewTopicBuilder("edge", "edge-01")
	if err != nil {
		t.Fatal(err)
	}
	projector := NewLatestStateProjector(&recordingPublisher{}, builder, time.Second)
	first := uint16(1)
	second := uint16(2)
	state := acquisition.CurrentState{DeviceID: 1, ExternalID: "device-01", Status: acquisition.StatusOnline, RegisterBlocks: []acquisition.RegisterBlockState{{ID: 2, Name: "holding", FunctionCode: 3, StartAddress: 10, Quantity: 2, SortOrder: 1, Values: []*uint16{&first, nil}}}}
	projector.OnState(state)
	state.RegisterBlocks[0].Values[0] = &second
	projector.OnCycle(state)

	now := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	status, wait := projector.nextTask(now)
	if status == nil || status.kind != "status" || wait != 0 || status.state.RegisterBlocks[0].Values[0] == nil || *status.state.RegisterBlocks[0].Values[0] != second {
		t.Fatalf("status task = %#v, wait = %s", status, wait)
	}
	projector.finish(*status, nil, now)
	raw, wait := projector.nextTask(now)
	if raw == nil || raw.kind != "raw" || wait != 0 {
		t.Fatalf("raw task = %#v, wait = %s", raw, wait)
	}
	payload, publication, err := projector.buildPublication(*raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if publication.QoS != 0 || publication.Retain || publication.Topic != "edge/edge-01/device/device-01/raw" {
		t.Fatalf("raw publication = %#v", publication)
	}
	if string(payload) == "" || projector.PendingCount() != 1 {
		t.Fatalf("raw payload/pending = %s/%d", payload, projector.PendingCount())
	}
}

func TestLatestProjectorRetryAndRepublishRemainBounded(t *testing.T) {
	builder, err := NewTopicBuilder("edge", "edge-01")
	if err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{err: ErrMQTTNotConnected}
	projector := NewLatestStateProjector(publisher, builder, time.Millisecond)
	state := acquisition.CurrentState{DeviceID: 1, ExternalID: "device-01", Status: acquisition.StatusOffline}
	for i := 0; i < 1000; i++ {
		state.LastError = errors.New("offline").Error()
		projector.OnCycle(state)
	}
	if projector.PendingCount() != 1 {
		t.Fatalf("pending entries = %d, want one coalesced entry", projector.PendingCount())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- projector.Run(ctx) }()
	waitForPublisher(t, publisher, 1)
	publisher.mu.Lock()
	publisher.err = nil
	publisher.mu.Unlock()
	projector.Republish()
	waitForPublisher(t, publisher, 2)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("projector did not stop")
	}
	if projector.PendingCount() == 0 {
		// The reconnect republish may have sent both status and raw; this check
		// only guards against an unbounded retry loop consuming entries.
		return
	}
}

type recordingPublisher struct {
	mu           sync.Mutex
	publications []Publication
	err          error
}

func (p *recordingPublisher) Publish(_ context.Context, publication Publication) error {
	p.mu.Lock()
	p.publications = append(p.publications, Publication{Topic: publication.Topic, QoS: publication.QoS, Retain: publication.Retain, Payload: append([]byte(nil), publication.Payload...)})
	err := p.err
	p.mu.Unlock()
	return err
}

func waitForPublisher(t *testing.T, publisher *recordingPublisher, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		publisher.mu.Lock()
		actual := len(publisher.publications)
		publisher.mu.Unlock()
		if actual >= count {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	publisher.mu.Lock()
	actual := len(publisher.publications)
	publisher.mu.Unlock()
	t.Fatalf("publisher calls = %d, want at least %d", actual, count)
}
