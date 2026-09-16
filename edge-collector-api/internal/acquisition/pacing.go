package acquisition

import (
	"context"
	"sync"
	"time"
)

type requestPacer struct {
	mu            sync.Mutex
	delay         time.Duration
	lastCompleted time.Time
	now           func() time.Time
	sleep         func(context.Context, time.Duration) error
}

func newRequestPacer(delay time.Duration) *requestPacer {
	return &requestPacer{
		delay: normalizeDelay(delay),
		now:   time.Now,
		sleep: sleepWithContext,
	}
}

func (p *requestPacer) SetDelay(delay time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.delay = normalizeDelay(delay)
}

func (p *requestPacer) run(ctx context.Context, operation func() ([]uint16, error)) ([]uint16, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.lastCompleted.IsZero() && p.delay > 0 {
		waitFor := p.lastCompleted.Add(p.delay).Sub(p.now())
		if waitFor > 0 {
			if err := p.sleep(ctx, waitFor); err != nil {
				return nil, err
			}
		}
	}
	registers, err := operation()
	p.lastCompleted = p.now()
	return registers, err
}

type pacedSession struct {
	underlying ModbusSession

	mu            sync.Mutex
	delay         time.Duration
	lastCompleted time.Time
	now           func() time.Time
	sleep         func(context.Context, time.Duration) error
	pacer         *requestPacer
}

func newPacedSession(underlying ModbusSession, delay time.Duration) *pacedSession {
	return &pacedSession{
		underlying: underlying,
		delay:      normalizeDelay(delay),
		now:        time.Now,
		sleep:      sleepWithContext,
	}
}

func (s *pacedSession) SetDelay(delay time.Duration) {
	if s.pacer != nil {
		s.pacer.SetDelay(delay)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay = normalizeDelay(delay)
}

func newPacedSessionWithPacer(underlying ModbusSession, pacer *requestPacer) *pacedSession {
	return &pacedSession{underlying: underlying, pacer: pacer}
}

func (s *pacedSession) Open() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.underlying.Open()
}

func (s *pacedSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.underlying.Close()
}

func (s *pacedSession) SetUnitID(id uint8) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.underlying.SetUnitID(id)
}

func (s *pacedSession) ReadHoldingRegisters(ctx context.Context, slaveID uint8, address, quantity uint16) ([]uint16, error) {
	return s.read(ctx, func() ([]uint16, error) {
		return s.underlying.ReadHoldingRegisters(ctx, slaveID, address, quantity)
	})
}

func (s *pacedSession) ReadInputRegisters(ctx context.Context, slaveID uint8, address, quantity uint16) ([]uint16, error) {
	return s.read(ctx, func() ([]uint16, error) {
		return s.underlying.ReadInputRegisters(ctx, slaveID, address, quantity)
	})
}

func (s *pacedSession) read(ctx context.Context, operation func() ([]uint16, error)) ([]uint16, error) {
	if s.pacer != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.pacer.run(ctx, operation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.lastCompleted.IsZero() && s.delay > 0 {
		waitFor := s.lastCompleted.Add(s.delay).Sub(s.now())
		if waitFor > 0 {
			if err := s.sleep(ctx, waitFor); err != nil {
				return nil, err
			}
		}
	}
	registers, err := operation()
	s.lastCompleted = s.now()
	return registers, err
}

func normalizeDelay(delay time.Duration) time.Duration {
	if delay < 0 {
		return 0
	}
	return delay
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
