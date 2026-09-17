package mqtt

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type pahoTokenFake struct {
	done chan struct{}
	err  error
}

func (f *pahoTokenFake) Wait() bool {
	<-f.done
	return true
}

func (f *pahoTokenFake) WaitTimeout(timeout time.Duration) bool {
	select {
	case <-f.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (f *pahoTokenFake) Done() <-chan struct{} { return f.done }

func (f *pahoTokenFake) Error() error { return f.err }

func TestWaitTokenReportsTimeoutBeforePUBACK(t *testing.T) {
	token := &pahoTokenFake{done: make(chan struct{})}
	err := waitToken(context.Background(), token, 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitToken() error = %v, want deadline exceeded", err)
	}
}

func TestWaitTokenPreservesContextCancellation(t *testing.T) {
	token := &pahoTokenFake{done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitToken(ctx, token, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitToken() error = %v, want context canceled", err)
	}
}

func TestParseMQTTBrokerURLRejectsUserInfo(t *testing.T) {
	_, err := parseMQTTBrokerURL("mqtt://operator:broker-password@example.test:1883")
	if err == nil {
		t.Fatal("parseMQTTBrokerURL() error = nil, want userinfo rejection")
	}
	if strings.Contains(err.Error(), "broker-password") {
		t.Fatalf("broker URL error leaked password: %v", err)
	}
}
