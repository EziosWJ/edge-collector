package acquisition

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

type ModbusSession interface {
	RegisterReader
	Open() error
	Close() error
	SetUnitID(uint8) error
}

type SessionFactory func(Channel) (ModbusSession, error)

type Runtime struct {
	channels []Channel
	devices  []Device
	store    *CurrentStateStore
	factory  SessionFactory
	logger   *log.Logger
}

func NewRuntime(channels []Channel, devices []Device, store *CurrentStateStore, factory SessionFactory, logger *log.Logger) (*Runtime, error) {
	if store == nil {
		return nil, fmt.Errorf("当前状态存储不能为空")
	}
	if factory == nil {
		return nil, fmt.Errorf("Modbus 会话工厂不能为空")
	}
	if logger == nil {
		logger = log.Default()
	}
	for _, device := range devices {
		store.Ensure(device)
	}
	return &Runtime{channels: channels, devices: devices, store: store, factory: factory, logger: logger}, nil
}

func (r *Runtime) Store() *CurrentStateStore { return r.store }

func (r *Runtime) Run(ctx context.Context) error {
	var wait sync.WaitGroup
	for _, channel := range r.channels {
		if channel.Enabled != Enabled {
			continue
		}
		channelDevices := devicesForChannel(r.devices, channel.ID)
		if len(channelDevices) == 0 {
			continue
		}
		wait.Add(1)
		go func(channel Channel, devices []Device) {
			defer wait.Done()
			r.runChannel(ctx, channel, devices)
		}(channel, channelDevices)
	}
	wait.Wait()
	return ctx.Err()
}

func PollChannelOnce(ctx context.Context, channel Channel, devices []Device, session ModbusSession, store *CurrentStateStore) error {
	if session == nil || store == nil {
		return fmt.Errorf("采集通道依赖不能为空")
	}
	for _, device := range devices {
		if device.Enabled != Enabled || device.ChannelID != channel.ID {
			continue
		}
		reading := FeedProtectorReading{}
		var readErr error
		if device.DeviceType != "" && device.DeviceType != DeviceTypeFeedProtector {
			readErr = fmt.Errorf("不支持的设备类型: %s", device.DeviceType)
		} else if err := session.SetUnitID(device.SlaveID); err != nil {
			readErr = fmt.Errorf("设置 Modbus 地址 %d 失败: %w", device.SlaveID, err)
		} else {
			reading, readErr = ReadFeedProtector(ctx, session, device.SlaveID)
		}
		store.Record(device, reading, readErr, time.Now().UTC())
	}
	return nil
}

func (r *Runtime) runChannel(ctx context.Context, channel Channel, devices []Device) {
	session, err := r.factory(channel)
	if err != nil {
		r.recordChannelFailure(devices, err)
		return
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			r.logger.Printf("关闭采集串口失败 channel_id=%d error=%v", channel.ID, closeErr)
		}
	}()

	opened := false
	nextDue := make(map[int64]time.Time, len(devices))
	for _, device := range devices {
		nextDue[device.ID] = time.Time{}
	}
	for {
		if !opened {
			if err := session.Open(); err != nil {
				r.recordChannelFailure(devices, err)
				if !waitContext(ctx, time.Second) {
					return
				}
				continue
			}
			opened = true
		}

		now := time.Now()
		waitFor := time.Second
		for _, device := range devices {
			if device.Enabled != Enabled {
				continue
			}
			due := nextDue[device.ID]
			if due.IsZero() || !now.Before(due) {
				_ = PollChannelOnce(ctx, channel, []Device{device}, session, r.store)
				interval := time.Duration(device.PollIntervalMS) * time.Millisecond
				if interval <= 0 {
					interval = time.Second
				}
				nextDue[device.ID] = time.Now().Add(interval)
				if interval < waitFor {
					waitFor = interval
				}
				continue
			}
			if remaining := time.Until(due); remaining < waitFor {
				waitFor = remaining
			}
		}
		if !waitContext(ctx, waitFor) {
			return
		}
	}
}

func (r *Runtime) recordChannelFailure(devices []Device, err error) {
	for _, device := range devices {
		r.store.Record(device, FeedProtectorReading{}, err, time.Now().UTC())
	}
}

func (s *CurrentStateStore) Ensure(device Device) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.states[device.ID]; ok {
		return
	}
	s.states[device.ID] = CurrentState{
		DeviceID:       device.ID,
		DeviceName:     device.Name,
		ChannelID:      device.ChannelID,
		SlaveID:        device.SlaveID,
		FieldValidity:  make(map[string]bool),
		FieldUpdatedAt: make(map[string]time.Time),
		Status:         StatusInitial,
	}
}

func devicesForChannel(devices []Device, channelID int64) []Device {
	result := make([]Device, 0)
	for _, device := range devices {
		if device.ChannelID == channelID {
			result = append(result, device)
		}
	}
	return result
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return true
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
