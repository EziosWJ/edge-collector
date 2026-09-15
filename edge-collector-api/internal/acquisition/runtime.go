package acquisition

import (
	"context"
	"fmt"
	"log"
	"sort"
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

type ConfigurationLoader func(context.Context) ([]Channel, []Device, error)

type configurationSnapshot struct {
	channels []Channel
	devices  []Device
}

type channelConfig struct {
	active  bool
	channel Channel
	devices []Device
}

type Runtime struct {
	mu       sync.Mutex
	channels []Channel
	devices  []Device
	loader   ConfigurationLoader

	store   *CurrentStateStore
	factory SessionFactory
	logger  *log.Logger

	refreshCh chan configurationSnapshot
	started   bool
	retry     bool
}

func NewRuntime(channels []Channel, devices []Device, store *CurrentStateStore, factory SessionFactory, logger *log.Logger, loaders ...ConfigurationLoader) (*Runtime, error) {
	if store == nil {
		return nil, fmt.Errorf("当前状态存储不能为空")
	}
	if factory == nil {
		return nil, fmt.Errorf("Modbus 会话工厂不能为空")
	}
	if logger == nil {
		logger = log.Default()
	}
	var loader ConfigurationLoader
	if len(loaders) > 0 {
		loader = loaders[0]
	}
	return &Runtime{
		channels:  cloneChannels(channels),
		devices:   cloneDevices(devices),
		loader:    loader,
		store:     store,
		factory:   factory,
		logger:    logger,
		refreshCh: make(chan configurationSnapshot, 1),
	}, nil
}

func (r *Runtime) Store() *CurrentStateStore { return r.store }

// Refresh reloads the committed enabled configuration and hands it to the
// runtime coordinator. The coordinator applies it only at channel boundaries;
// a request currently in flight is never interrupted.
func (r *Runtime) Refresh(ctx context.Context) error {
	if r.loader == nil {
		return fmt.Errorf("采集运行时配置加载器不能为空")
	}
	channels, devices, err := r.loader(ctx)
	if err != nil {
		r.logger.Printf("加载采集运行时配置失败: %v", err)
		r.mu.Lock()
		r.retry = r.started
		r.mu.Unlock()
		return err
	}
	snapshot := configurationSnapshot{channels: cloneChannels(channels), devices: cloneDevices(devices)}

	r.mu.Lock()
	r.channels = cloneChannels(snapshot.channels)
	r.devices = cloneDevices(snapshot.devices)
	r.retry = false
	started := r.started
	r.mu.Unlock()
	if !started {
		return nil
	}

	// Keep only the latest committed snapshot if several management writes
	// arrive while the coordinator is applying an earlier one.
	select {
	case <-r.refreshCh:
	default:
	}
	select {
	case r.refreshCh <- snapshot:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) Run(ctx context.Context) error {
	r.mu.Lock()
	r.started = true
	initial := configurationSnapshot{channels: cloneChannels(r.channels), devices: cloneDevices(r.devices)}
	r.mu.Unlock()

	var runners sync.Map
	var runnerWait sync.WaitGroup
	retryTicker := time.NewTicker(time.Second)
	defer retryTicker.Stop()
	apply := func(snapshot configurationSnapshot) {
		active := activeChannelConfigs(snapshot.channels, snapshot.devices)
		activeIDs := make(map[int64]struct{}, len(active))
		for channelID, config := range active {
			activeIDs[channelID] = struct{}{}
			if existing, ok := runners.Load(channelID); ok {
				existing.(*channelRunner).Update(config)
				continue
			}
			runner := newChannelRunner(r, config)
			runners.Store(channelID, runner)
			runnerWait.Add(1)
			go func(runner *channelRunner) {
				defer runnerWait.Done()
				runner.Run(ctx)
			}(runner)
		}
		runners.Range(func(key, value any) bool {
			channelID := key.(int64)
			if _, ok := activeIDs[channelID]; !ok {
				runner := value.(*channelRunner)
				runner.Stop()
				runners.Delete(channelID)
			}
			return true
		})
	}

	apply(initial)
	for {
		select {
		case snapshot := <-r.refreshCh:
			apply(snapshot)
		case <-retryTicker.C:
			r.mu.Lock()
			retry := r.retry
			r.mu.Unlock()
			if retry {
				_ = r.Refresh(ctx)
			}
		case <-ctx.Done():
			r.mu.Lock()
			r.started = false
			r.mu.Unlock()
			runners.Range(func(_, value any) bool {
				value.(*channelRunner).Stop()
				return true
			})
			runnerWait.Wait()
			return ctx.Err()
		}
	}
}

type channelRunner struct {
	runtime *Runtime
	updates chan channelConfig
	stop    chan struct{}
}

func newChannelRunner(runtime *Runtime, initial channelConfig) *channelRunner {
	runner := &channelRunner{
		runtime: runtime,
		updates: make(chan channelConfig, 1),
		stop:    make(chan struct{}),
	}
	runner.Update(initial)
	return runner
}

func (r *channelRunner) Update(config channelConfig) {
	select {
	case <-r.stop:
		return
	default:
	}
	select {
	case <-r.updates:
	default:
	}
	select {
	case r.updates <- cloneChannelConfig(config):
	case <-r.stop:
	}
}

func (r *channelRunner) Stop() {
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
}

func (r *channelRunner) Run(ctx context.Context) {
	current := channelConfig{}
	nextDue := make(map[int64]time.Time)
	var session *pacedSession
	removeCurrentStates := func() {
		for _, device := range current.devices {
			r.runtime.store.Remove(device.ID)
		}
	}

	closeSession := func() {
		if session == nil {
			return
		}
		if err := session.Close(); err != nil {
			r.runtime.logger.Printf("关闭采集串口失败 channel_id=%d error=%v", current.channel.ID, err)
		}
		session = nil
	}

	apply := func(next channelConfig) {
		if current.active && (!next.active || !samePhysicalChannel(current.channel, next.channel)) {
			closeSession()
		}

		oldDevices := make(map[int64]Device, len(current.devices))
		for _, device := range current.devices {
			oldDevices[device.ID] = device
		}
		newDevices := make(map[int64]Device, len(next.devices))
		for _, device := range next.devices {
			newDevices[device.ID] = device
			r.runtime.store.Ensure(device)
			if _, exists := oldDevices[device.ID]; !exists {
				nextDue[device.ID] = time.Time{}
			}
		}
		for deviceID := range oldDevices {
			if _, exists := newDevices[deviceID]; !exists {
				delete(nextDue, deviceID)
				r.runtime.store.Remove(deviceID)
			}
		}
		for deviceID := range nextDue {
			if _, exists := newDevices[deviceID]; !exists {
				delete(nextDue, deviceID)
			}
		}

		if !next.active {
			closeSession()
			for deviceID := range oldDevices {
				r.runtime.store.Remove(deviceID)
			}
			nextDue = make(map[int64]time.Time)
		}
		if session != nil {
			session.SetDelay(time.Duration(next.channel.InterRequestDelayMS) * time.Millisecond)
		}
		current = cloneChannelConfig(next)
	}

	initial, ok := takeLatestConfig(r.updates)
	if !ok {
		return
	}
	apply(initial)

	for {
		select {
		case <-ctx.Done():
			removeCurrentStates()
			closeSession()
			return
		case <-r.stop:
			removeCurrentStates()
			closeSession()
			return
		default:
		}

		if config, ok := takeLatestConfig(r.updates); ok {
			apply(config)
		}
		if !current.active || len(current.devices) == 0 {
			result := waitForRunnerEvent(ctx, r.stop, r.updates, 0)
			if result.stopped {
				removeCurrentStates()
				closeSession()
				return
			}
			apply(result.config)
			continue
		}

		if session == nil {
			raw, err := r.runtime.factory(current.channel)
			if err == nil && raw == nil {
				err = fmt.Errorf("Modbus 会话工厂返回空会话")
			}
			if err != nil {
				r.runtime.recordChannelFailure(current.devices, err)
				result := waitForRunnerEvent(ctx, r.stop, r.updates, time.Second)
				if result.stopped {
					removeCurrentStates()
					return
				}
				if result.updated {
					apply(result.config)
				}
				continue
			}

			session = newPacedSession(raw, time.Duration(current.channel.InterRequestDelayMS)*time.Millisecond)
			if err := session.Open(); err != nil {
				r.runtime.recordChannelFailure(current.devices, err)
				if closeErr := session.Close(); closeErr != nil {
					r.runtime.logger.Printf("关闭未打开的采集串口失败 channel_id=%d error=%v", current.channel.ID, closeErr)
				}
				session = nil
				result := waitForRunnerEvent(ctx, r.stop, r.updates, time.Second)
				if result.stopped {
					removeCurrentStates()
					return
				}
				if result.updated {
					apply(result.config)
				}
				continue
			}
		}

		device, due, waitFor := nextDevice(current.devices, nextDue, time.Now())
		if !due {
			result := waitForRunnerEvent(ctx, r.stop, r.updates, waitFor)
			if result.stopped {
				removeCurrentStates()
				closeSession()
				return
			}
			if result.updated {
				apply(result.config)
			}
			continue
		}

		pollDeviceOnce(ctx, current.channel, device, session, r.runtime.store)
		interval := time.Duration(device.PollIntervalMS) * time.Millisecond
		if interval <= 0 {
			interval = time.Second
		}
		// The next period starts after the complete two-request device read.
		nextDue[device.ID] = time.Now().Add(interval)
	}
}

type runnerWaitResult struct {
	config  channelConfig
	updated bool
	stopped bool
}

func waitForRunnerEvent(ctx context.Context, stop <-chan struct{}, updates <-chan channelConfig, duration time.Duration) runnerWaitResult {
	if duration <= 0 {
		select {
		case <-ctx.Done():
			return runnerWaitResult{stopped: true}
		case <-stop:
			return runnerWaitResult{stopped: true}
		case config := <-updates:
			return runnerWaitResult{config: config, updated: true}
		}
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return runnerWaitResult{stopped: true}
	case <-stop:
		return runnerWaitResult{stopped: true}
	case config := <-updates:
		return runnerWaitResult{config: config, updated: true}
	case <-timer.C:
		return runnerWaitResult{}
	}
}

func (r *Runtime) recordChannelFailure(devices []Device, err error) {
	for _, device := range devices {
		reads := make([]RegisterBlockRead, len(device.RegisterBlocks))
		for index, block := range device.RegisterBlocks {
			reads[index] = RegisterBlockRead{Block: block, Err: err}
		}
		r.store.RecordCycle(device, reads, time.Now().UTC())
	}
	r.logger.Printf("采集通道不可用 error=%v", err)
}

func PollChannelOnce(ctx context.Context, channel Channel, devices []Device, session ModbusSession, store *CurrentStateStore) error {
	if session == nil || store == nil {
		return fmt.Errorf("采集通道依赖不能为空")
	}
	for _, device := range devices {
		if device.Enabled != Enabled || device.ChannelID != channel.ID {
			continue
		}
		pollDeviceOnce(ctx, channel, device, session, store)
	}
	return nil
}

func pollDeviceOnce(ctx context.Context, _ Channel, device Device, session ModbusSession, store *CurrentStateStore) {
	reads := make([]RegisterBlockRead, 0, len(device.RegisterBlocks))
	var setupErr error
	if device.DeviceType != "" && device.DeviceType != DeviceTypeFeedProtector {
		setupErr = fmt.Errorf("不支持的设备类型: %s", device.DeviceType)
	} else if err := session.SetUnitID(device.SlaveID); err != nil {
		setupErr = fmt.Errorf("设置 Modbus 地址 %d 失败: %w", device.SlaveID, err)
	}
	for _, block := range device.RegisterBlocks {
		read := RegisterBlockRead{Block: block}
		if setupErr != nil {
			read.Err = setupErr
		} else {
			read.Values, read.Err = readRegisterBlock(ctx, session, device.SlaveID, block)
		}
		reads = append(reads, read)
	}
	store.RecordCycle(device, reads, time.Now().UTC())
}

func activeChannelConfigs(channels []Channel, devices []Device) map[int64]channelConfig {
	result := make(map[int64]channelConfig)
	for _, channel := range channels {
		if channel.Enabled != Enabled {
			continue
		}
		channelDevices := devicesForChannel(devices, channel.ID)
		if len(channelDevices) == 0 {
			continue
		}
		result[channel.ID] = channelConfig{active: true, channel: channel, devices: channelDevices}
	}
	return result
}

func devicesForChannel(devices []Device, channelID int64) []Device {
	result := make([]Device, 0)
	for _, device := range devices {
		if device.ChannelID == channelID && device.Enabled == Enabled && len(device.RegisterBlocks) > 0 {
			result = append(result, device)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func nextDevice(devices []Device, nextDue map[int64]time.Time, now time.Time) (Device, bool, time.Duration) {
	candidates := append([]Device(nil), devices...)
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := nextDue[candidates[i].ID], nextDue[candidates[j].ID]
		if left.IsZero() != right.IsZero() {
			return left.IsZero()
		}
		if !left.Equal(right) {
			return left.Before(right)
		}
		return candidates[i].ID < candidates[j].ID
	})

	waitFor := time.Duration(0)
	for _, device := range candidates {
		due := nextDue[device.ID]
		if due.IsZero() || !now.Before(due) {
			return device, true, 0
		}
		remaining := due.Sub(now)
		if waitFor == 0 || remaining < waitFor {
			waitFor = remaining
		}
	}
	if waitFor < 0 {
		waitFor = 0
	}
	return Device{}, false, waitFor
}

func samePhysicalChannel(left, right Channel) bool {
	return left.Port == right.Port && left.BaudRate == right.BaudRate && left.DataBits == right.DataBits && left.StopBits == right.StopBits && left.Parity == right.Parity && left.TimeoutMS == right.TimeoutMS
}

func cloneChannels(channels []Channel) []Channel {
	return append([]Channel(nil), channels...)
}

func cloneDevices(devices []Device) []Device {
	result := append([]Device(nil), devices...)
	for index := range result {
		result[index].RegisterBlocks = append([]RegisterBlock(nil), result[index].RegisterBlocks...)
	}
	return result
}

func cloneChannelConfig(config channelConfig) channelConfig {
	config.devices = cloneDevices(config.devices)
	return config
}

func takeLatestConfig(updates <-chan channelConfig) (channelConfig, bool) {
	select {
	case config := <-updates:
		for {
			select {
			case latest := <-updates:
				config = latest
			default:
				return config, true
			}
		}
	default:
		return channelConfig{}, false
	}
}
