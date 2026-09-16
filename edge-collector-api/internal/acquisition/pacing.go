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

	if err := p.waitLocked(ctx); err != nil {
		return nil, err
	}
	registers, err := operation()
	p.lastCompleted = p.now()
	return registers, err
}

func (p *requestPacer) runError(ctx context.Context, operation func() error) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.waitLocked(ctx); err != nil {
		return err
	}
	err := operation()
	p.lastCompleted = p.now()
	return err
}

func (p *requestPacer) waitLocked(ctx context.Context) error {
	if !p.lastCompleted.IsZero() && p.delay > 0 {
		waitFor := p.lastCompleted.Add(p.delay).Sub(p.now())
		if waitFor > 0 {
			return p.sleep(ctx, waitFor)
		}
	}
	return nil
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

func (s *pacedSession) WriteRegisters(ctx context.Context, slaveID uint8, address uint16, values []uint16) error {
	writer, ok := s.underlying.(ModbusWriter)
	if !ok {
		return ErrModbusWriterUnavailable
	}
	copyValues := append([]uint16(nil), values...)
	return s.write(ctx, func() error {
		return writer.WriteRegisters(ctx, slaveID, address, copyValues)
	})
}

func (s *pacedSession) WriteCoil(ctx context.Context, slaveID uint8, address uint16, on bool) error {
	writer, ok := s.underlying.(ModbusWriter)
	if !ok {
		return ErrModbusWriterUnavailable
	}
	return s.write(ctx, func() error {
		return writer.WriteCoil(ctx, slaveID, address, on)
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

func (s *pacedSession) write(ctx context.Context, operation func() error) error {
	if s.pacer != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.pacer.runError(ctx, operation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.lastCompleted.IsZero() && s.delay > 0 {
		waitFor := s.lastCompleted.Add(s.delay).Sub(s.now())
		if waitFor > 0 {
			if err := s.sleep(ctx, waitFor); err != nil {
				return err
			}
		}
	}
	err := operation()
	s.lastCompleted = s.now()
	return err
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
