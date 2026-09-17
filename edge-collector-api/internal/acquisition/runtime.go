package acquisition

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition/script"
)

type ModbusSession interface {
	RegisterReader
	Open() error
	Close() error
	SetUnitID(uint8) error
}

// ScriptVersionProvider resolves the published version selected for a device.
// Runtime.Refresh calls it while building a configuration snapshot. A nil
// version means that the device is unbound or has no published version; drafts
// must never be returned here.
type ScriptVersionProvider func(context.Context, Device) (*ScriptVersion, error)

// ScriptExecutor is the script package's public execution seam. A
// *script.Runtime satisfies it directly; the acquisition runtime only selects
// the immutable version and supplies a transport-neutral host.
type ScriptExecutor interface {
	Execute(context.Context, script.ScriptVersion, script.Invocation, script.Host) (script.Result, error)
}

// ScriptCommandExecutor is the optional command entrypoint seam. The
// acquisition runner supplies a Host only at a channel-safe boundary; MQTT
// intake never receives or calls this interface directly.
type ScriptCommandExecutor interface {
	ExecuteCommand(context.Context, script.ScriptVersion, script.Invocation, script.Host, string, any) (script.Result, error)
}

type ScriptCommandCapability interface {
	CommandAvailable(context.Context, script.ScriptVersion) (bool, error)
}

type ScriptEventSink func(context.Context, Device, ScriptVersion, []script.Event)

// StateCycleSink is called after static acquisition and after_poll have both
// completed and the CurrentState snapshot is committed. Implementations must
// keep this callback bounded and non-blocking for the acquisition runner.
type StateCycleSink func(context.Context, CurrentState)

// ScriptStateResetter is the state lifecycle part of script.Runtime. It is
// optional for fakes that do not retain state.
type ScriptStateResetter interface {
	Reset(script.StateScope)
}

// ScriptStateSnapshotter exposes committed script state for the management
// observation API. *script.Runtime implements it; test executors may omit it.
type ScriptStateSnapshotter interface {
	Snapshot(script.StateScope) script.StateSnapshot
}

type RuntimeScriptConfig struct {
	VersionProvider ScriptVersionProvider
	Executor        ScriptExecutor
	EventSink       ScriptEventSink
	CycleSink       StateCycleSink
}

func newScriptHostError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	class := script.ErrorClassModbusTransport
	if errors.Is(err, ErrModbusWriterUnavailable) {
		class = script.ErrorClassHostUnavailable
	} else if isModbusExceptionError(err) {
		class = script.ErrorClassModbusException
	}
	return script.NewHostError(class, fmt.Errorf("%s: %w", operation, err))
}

type SessionFactory func(Channel, Device) (ModbusSession, error)

type ConfigurationLoader func(context.Context) ([]Channel, []Device, error)

type configurationSnapshot struct {
	channels       []Channel
	devices        []Device
	scriptVersions map[int64]ScriptVersion
}

type channelConfig struct {
	active         bool
	channel        Channel
	devices        []Device
	scriptVersions map[int64]ScriptVersion
}

type Runtime struct {
	mu             sync.Mutex
	channels       []Channel
	devices        []Device
	scriptVersions map[int64]ScriptVersion
	loader         ConfigurationLoader

	store   *CurrentStateStore
	factory SessionFactory
	logger  *log.Logger
	scripts RuntimeScriptConfig

	runnersMu            sync.RWMutex
	runners              map[int64]*channelRunner
	commandQueueCapacity int
	commandPollFairness  int

	scriptStateMu sync.RWMutex
	scriptStates  map[int64]ScriptRuntimeState

	refreshCh     chan configurationSnapshot
	started       bool
	retry         bool
	snapshotReady bool
}

func NewRuntime(channels []Channel, devices []Device, store *CurrentStateStore, factory SessionFactory, logger *log.Logger, loaders ...ConfigurationLoader) (*Runtime, error) {
	return newRuntime(channels, devices, store, factory, logger, RuntimeScriptConfig{}, loaders...)
}

// NewRuntimeWithScripts is the #34 integration constructor. The regular
// NewRuntime remains source-compatible for deployments that have not enabled
// the script management/runtime adapter yet.
func NewRuntimeWithScripts(channels []Channel, devices []Device, store *CurrentStateStore, factory SessionFactory, logger *log.Logger, scripts RuntimeScriptConfig, loaders ...ConfigurationLoader) (*Runtime, error) {
	return newRuntime(channels, devices, store, factory, logger, scripts, loaders...)
}

func newRuntime(channels []Channel, devices []Device, store *CurrentStateStore, factory SessionFactory, logger *log.Logger, scripts RuntimeScriptConfig, loaders ...ConfigurationLoader) (*Runtime, error) {
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
		channels:             cloneChannels(channels),
		devices:              cloneDevices(devices),
		scriptVersions:       make(map[int64]ScriptVersion),
		loader:               loader,
		store:                store,
		factory:              factory,
		logger:               logger,
		scripts:              scripts,
		scriptStates:         make(map[int64]ScriptRuntimeState),
		refreshCh:            make(chan configurationSnapshot, 1),
		runners:              make(map[int64]*channelRunner),
		commandQueueCapacity: 32,
		commandPollFairness:  1,
	}, nil
}

func (r *Runtime) Store() *CurrentStateStore { return r.store }

func (r *Runtime) SetScriptRuntime(scripts RuntimeScriptConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scripts = scripts
}

func (r *Runtime) scriptConfig() RuntimeScriptConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.scripts
}

type capturedScriptCycle struct {
	invocation script.Invocation
	version    script.ScriptVersion
	identity   ScriptStateIdentity
	executor   ScriptExecutor
	enabled    bool
}

func (r *Runtime) captureScriptCycle(channel Channel, device Device, versions map[int64]ScriptVersion) (capturedScriptCycle, error) {
	config := r.scriptConfig()
	if config.Executor == nil || device.ScriptID == nil {
		return capturedScriptCycle{}, nil
	}
	selected, ok := versions[device.ID]
	if !ok {
		return capturedScriptCycle{}, nil
	}
	if selected.ScriptID == 0 {
		selected.ScriptID = *device.ScriptID
	}
	if selected.ScriptID != *device.ScriptID {
		return capturedScriptCycle{}, fmt.Errorf("设备 %d 的脚本版本归属不匹配", device.ID)
	}
	scriptVersion := script.ScriptVersion{
		ScriptID:  selected.ScriptID,
		VersionID: selected.ID,
		VersionNo: selected.VersionNo,
		Source:    selected.Source,
		Checksum:  selected.Checksum,
	}
	return capturedScriptCycle{
		invocation: script.Invocation{
			DeviceID:  device.ID,
			ChannelID: channel.ID,
			UnitID:    device.UnitID,
			Protocol:  channel.Protocol,
		},
		version:  scriptVersion,
		identity: scriptStateIdentity(channel, device, selected),
		executor: config.Executor,
		enabled:  true,
	}, nil
}

func (r *Runtime) loadScriptVersions(ctx context.Context, devices []Device) (map[int64]ScriptVersion, error) {
	config := r.scriptConfig()
	if config.VersionProvider == nil {
		return nil, nil
	}

	versionsByScriptID := make(map[int64]ScriptVersion)
	loadedScriptIDs := make(map[int64]bool)
	hasVersion := make(map[int64]bool)
	versionsByDeviceID := make(map[int64]ScriptVersion)
	for _, device := range devices {
		if device.ScriptID == nil {
			continue
		}
		scriptID := *device.ScriptID
		if !loadedScriptIDs[scriptID] {
			loadedScriptIDs[scriptID] = true
			version, err := config.VersionProvider(ctx, device)
			if err != nil {
				return nil, fmt.Errorf("加载设备 %d 的脚本发布版本失败: %w", device.ID, err)
			}
			if version != nil {
				selected := *version
				if selected.ScriptID == 0 {
					selected.ScriptID = scriptID
				}
				if selected.ScriptID != scriptID {
					return nil, fmt.Errorf("设备 %d 的脚本版本归属不匹配", device.ID)
				}
				versionsByScriptID[scriptID] = selected
				hasVersion[scriptID] = true
			}
		}
		if hasVersion[scriptID] {
			versionsByDeviceID[device.ID] = versionsByScriptID[scriptID]
		}
	}
	return versionsByDeviceID, nil
}

func (r *Runtime) resetScriptState(identity ScriptStateIdentity) {
	r.scriptStateMu.Lock()
	if state, ok := r.scriptStates[identity.DeviceID]; ok &&
		state.ScriptID == identity.ScriptID && state.ScriptVersionID == identity.ScriptVersionID {
		delete(r.scriptStates, identity.DeviceID)
	}
	r.scriptStateMu.Unlock()

	config := r.scriptConfig()
	resetter, ok := config.Executor.(ScriptStateResetter)
	if !ok {
		return
	}
	resetter.Reset(script.StateScope{DeviceID: identity.DeviceID, ScriptID: identity.ScriptID, ScriptVersionID: identity.ScriptVersionID})
}

type deviceScriptHost struct {
	session         *pacedSession
	unitID          uint8
	raw             map[rawRegisterKey]uint16
	operationLogger scriptModbusOperationLogger
}

type scriptModbusOperation struct {
	FunctionCode int
	Address      uint16
	Quantity     int
	Err          error
}

type scriptModbusOperationLogger func(scriptModbusOperation)

func (h *deviceScriptHost) logOperation(operation scriptModbusOperation) {
	if h.operationLogger != nil {
		h.operationLogger(operation)
	}
}

type rawRegisterKey struct {
	functionCode int
	address      uint16
}

func newDeviceScriptHost(session *pacedSession, unitID uint8, reads []RegisterBlockRead, loggers ...scriptModbusOperationLogger) *deviceScriptHost {
	raw := make(map[rawRegisterKey]uint16)
	for _, read := range reads {
		if read.Err != nil || len(read.Values) != read.Block.Quantity || read.Block.StartAddress < 0 || read.Block.StartAddress > 65535 {
			continue
		}
		for index, value := range read.Values {
			address := read.Block.StartAddress + index
			if address > 65535 {
				break
			}
			key := rawRegisterKey{functionCode: read.Block.FunctionCode, address: uint16(address)}
			if _, exists := raw[key]; !exists {
				raw[key] = value
			}
		}
	}
	var operationLogger scriptModbusOperationLogger
	if len(loggers) > 0 {
		operationLogger = loggers[0]
	}
	return &deviceScriptHost{session: session, unitID: unitID, raw: raw, operationLogger: operationLogger}
}

func (h *deviceScriptHost) RawRegister(functionCode int, address uint16) (uint16, bool) {
	value, ok := h.raw[rawRegisterKey{functionCode: functionCode, address: address}]
	return value, ok
}

func (h *deviceScriptHost) ReadHolding(ctx context.Context, address, quantity uint16) ([]uint16, error) {
	if err := validateDynamicRead(address, quantity); err != nil {
		return nil, err
	}
	values, err := h.session.ReadHoldingRegisters(ctx, h.unitID, address, quantity)
	if err != nil {
		h.logOperation(scriptModbusOperation{FunctionCode: 3, Address: address, Quantity: int(quantity), Err: err})
		return nil, newScriptHostError("read_holding", err)
	}
	if len(values) != int(quantity) {
		err := fmt.Errorf("返回寄存器数量为 %d，期望 %d", len(values), quantity)
		h.logOperation(scriptModbusOperation{FunctionCode: 3, Address: address, Quantity: int(quantity), Err: err})
		return nil, newScriptHostError("read_holding", err)
	}
	h.logOperation(scriptModbusOperation{FunctionCode: 3, Address: address, Quantity: int(quantity)})
	return append([]uint16(nil), values...), nil
}

func (h *deviceScriptHost) WriteRegisters(ctx context.Context, address uint16, values []uint16) error {
	if err := validateDynamicWrite(address, values); err != nil {
		return err
	}
	err := h.session.WriteRegisters(ctx, h.unitID, address, values)
	h.logOperation(scriptModbusOperation{FunctionCode: 16, Address: address, Quantity: len(values), Err: err})
	if err != nil {
		return newScriptHostError("write_registers", err)
	}
	return nil
}

func (h *deviceScriptHost) WriteCoil(ctx context.Context, address uint16, on bool) error {
	err := h.session.WriteCoil(ctx, h.unitID, address, on)
	h.logOperation(scriptModbusOperation{FunctionCode: 5, Address: address, Quantity: 1, Err: err})
	if err != nil {
		return newScriptHostError("write_coil", err)
	}
	return nil
}

func (h *deviceScriptHost) Delay(ctx context.Context, duration time.Duration) error {
	if duration < 0 {
		return fmt.Errorf("脚本 delay 不能为负数")
	}
	return sleepWithContext(ctx, duration)
}

func validateDynamicRead(address, quantity uint16) error {
	if quantity < 1 || quantity > 125 || int(address)+int(quantity) > 65536 {
		return fmt.Errorf("脚本 FC03 读取参数超出范围")
	}
	return nil
}

func validateDynamicWrite(address uint16, values []uint16) error {
	if len(values) < 1 || len(values) > 123 || int(address)+len(values) > 65536 {
		return fmt.Errorf("脚本 FC16 写入参数超出范围")
	}
	return nil
}

type deviceCycleResult struct {
	staticErr            error
	staticReads          []RegisterBlockRead
	scriptErr            error
	scriptTransportError bool
	scriptResult         script.Result
	scriptExecuted       bool
}

func (r *Runtime) executeDeviceCycle(ctx context.Context, channel Channel, device Device, session *pacedSession, stopOnError bool, cycle capturedScriptCycle) deviceCycleResult {
	reads := make([]RegisterBlockRead, 0, len(device.RegisterBlocks))
	staticErr := pollDeviceCycleWithReads(ctx, device, session, r.store, stopOnError, &reads)
	result := deviceCycleResult{staticErr: staticErr, staticReads: append([]RegisterBlockRead(nil), reads...)}
	if !cycle.enabled || cycle.executor == nil || ctx.Err() != nil {
		return result
	}
	if staticErr != nil && !isModbusExceptionError(staticErr) {
		return result
	}
	host := newDeviceScriptHost(session, device.UnitID, reads, func(operation scriptModbusOperation) {
		if operation.Err == nil {
			r.logger.Printf("acquisition_script_modbus device_id=%d script_id=%d script_version_id=%d version_no=%d channel_id=%d function_code=%d address=%d quantity=%d result=OK", device.ID, cycle.version.ScriptID, cycle.version.VersionID, cycle.version.VersionNo, channel.ID, operation.FunctionCode, operation.Address, operation.Quantity)
			return
		}
		r.logger.Printf("acquisition_script_modbus device_id=%d script_id=%d script_version_id=%d version_no=%d channel_id=%d function_code=%d address=%d quantity=%d result=ERROR error=%q", device.ID, cycle.version.ScriptID, cycle.version.VersionID, cycle.version.VersionNo, channel.ID, operation.FunctionCode, operation.Address, operation.Quantity, operation.Err)
	})
	result.scriptResult, result.scriptErr = cycle.executor.Execute(ctx, cycle.version, cycle.invocation, host)
	result.scriptExecuted = true
	result.scriptTransportError = script.ClassOf(result.scriptErr) == script.ErrorClassModbusTransport
	return result
}

// ListScriptRuntimeStates returns only observations produced by actual script
// invocations. It intentionally does not synthesize rows for configured
// devices that have not executed after_poll yet.
func (r *Runtime) ListScriptRuntimeStates(ctx context.Context) ([]ScriptRuntimeState, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	r.scriptStateMu.RLock()
	values := make([]ScriptRuntimeState, 0, len(r.scriptStates))
	for _, value := range r.scriptStates {
		values = append(values, cloneScriptRuntimeState(value))
	}
	r.scriptStateMu.RUnlock()
	sort.Slice(values, func(i, j int) bool { return values[i].DeviceID < values[j].DeviceID })
	return values, nil
}

// List implements ScriptRuntimeStateReader for the service adapter.
func (r *Runtime) List(ctx context.Context) ([]ScriptRuntimeState, error) {
	return r.ListScriptRuntimeStates(ctx)
}

func (r *Runtime) FindScriptRuntimeState(ctx context.Context, deviceID int64) (*ScriptRuntimeState, error) {
	if deviceID < 1 {
		return nil, ErrInvalid
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	r.scriptStateMu.RLock()
	value, ok := r.scriptStates[deviceID]
	if ok {
		value = cloneScriptRuntimeState(value)
	}
	r.scriptStateMu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	return &value, nil
}

// Get implements ScriptRuntimeStateReader for the service adapter.
func (r *Runtime) Get(ctx context.Context, deviceID int64) (*ScriptRuntimeState, error) {
	return r.FindScriptRuntimeState(ctx, deviceID)
}

func (r *Runtime) recordScriptRuntimeState(device Device, cycle capturedScriptCycle, result deviceCycleResult, at time.Time) {
	if !result.scriptExecuted || !cycle.enabled || cycle.executor == nil {
		return
	}

	value := ScriptRuntimeState{
		DeviceID:        device.ID,
		DeviceName:      device.Name,
		ScriptID:        cycle.version.ScriptID,
		ScriptVersionID: cycle.version.VersionID,
		VersionNo:       cycle.version.VersionNo,
		State:           make(map[string]any),
		Events:          make([]ScriptRuntimeEvent, 0),
	}

	r.scriptStateMu.RLock()
	previous, exists := r.scriptStates[device.ID]
	r.scriptStateMu.RUnlock()
	if exists && previous.ScriptID == value.ScriptID && previous.ScriptVersionID == value.ScriptVersionID {
		value.State = cloneScriptState(previous.State)
		value.Events = cloneScriptRuntimeEvents(previous.Events)
		value.LastSuccessAt = cloneTimePointer(previous.LastSuccessAt)
	}

	if snapshotter, ok := cycle.executor.(ScriptStateSnapshotter); ok {
		snapshot := snapshotter.Snapshot(script.StateScope{
			DeviceID: device.ID, ScriptID: cycle.version.ScriptID, ScriptVersionID: cycle.version.VersionID,
		})
		value.State = cloneScriptState(snapshot.State)
		value.Events = scriptEventsFromSnapshot(snapshot.Events)
	} else if result.scriptErr == nil {
		value.State = cloneScriptState(result.scriptResult.State)
		value.Events = scriptEventsFromResult(result.scriptResult.Events)
	}

	value.LastAttemptAt = cloneTimePointer(&at)
	if result.scriptErr == nil {
		value.LastSuccessAt = cloneTimePointer(&at)
		value.LastError = nil
		value.LastErrorType = nil
	} else {
		message := result.scriptErr.Error()
		value.LastError = &message
		if class := script.ClassOf(result.scriptErr); class != "" {
			classified := string(class)
			value.LastErrorType = &classified
		} else {
			value.LastErrorType = nil
		}
	}

	r.scriptStateMu.Lock()
	r.scriptStates[device.ID] = value
	r.scriptStateMu.Unlock()
}

func scriptEventsFromSnapshot(events []script.Event) []ScriptRuntimeEvent {
	result := make([]ScriptRuntimeEvent, 0, len(events))
	for _, event := range events {
		result = append(result, ScriptRuntimeEvent{Kind: event.Kind, Key: event.Key, Payload: cloneScriptValue(event.Payload), OccurredAt: event.At})
	}
	return result
}

func scriptEventsFromResult(events []script.Event) []ScriptRuntimeEvent {
	return scriptEventsFromSnapshot(events)
}

func cloneScriptRuntimeState(value ScriptRuntimeState) ScriptRuntimeState {
	value.State = cloneScriptState(value.State)
	value.Events = cloneScriptRuntimeEvents(value.Events)
	value.LastAttemptAt = cloneTimePointer(value.LastAttemptAt)
	value.LastSuccessAt = cloneTimePointer(value.LastSuccessAt)
	value.LastError = cloneStringPointer(value.LastError)
	value.LastErrorType = cloneStringPointer(value.LastErrorType)
	return value
}

func cloneScriptRuntimeEvents(events []ScriptRuntimeEvent) []ScriptRuntimeEvent {
	result := make([]ScriptRuntimeEvent, 0, len(events))
	for _, event := range events {
		event.Payload = cloneScriptValue(event.Payload)
		result = append(result, event)
	}
	return result
}

func cloneScriptState(state map[string]any) map[string]any {
	result := make(map[string]any, len(state))
	for key, value := range state {
		result[key] = cloneScriptValue(value)
	}
	return result
}

func cloneScriptValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneScriptState(value)
	case []any:
		result := make([]any, len(value))
		for index, item := range value {
			result[index] = cloneScriptValue(item)
		}
		return result
	default:
		return value
	}
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

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
	scriptVersions, err := r.loadScriptVersions(ctx, devices)
	if err != nil {
		r.logger.Printf("加载采集运行时脚本版本失败: %v", err)
		r.mu.Lock()
		r.retry = r.started
		r.mu.Unlock()
		return err
	}
	snapshot := configurationSnapshot{
		channels:       cloneChannels(channels),
		devices:        cloneDevices(devices),
		scriptVersions: cloneScriptVersions(scriptVersions),
	}

	r.mu.Lock()
	r.channels = cloneChannels(snapshot.channels)
	r.devices = cloneDevices(snapshot.devices)
	r.scriptVersions = cloneScriptVersions(snapshot.scriptVersions)
	r.retry = false
	r.snapshotReady = true
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
	needsRefresh := r.loader != nil && !r.snapshotReady
	r.mu.Unlock()
	if needsRefresh {
		if err := r.Refresh(ctx); err != nil {
			return err
		}
	}

	r.mu.Lock()
	r.started = true
	initial := configurationSnapshot{
		channels:       cloneChannels(r.channels),
		devices:        cloneDevices(r.devices),
		scriptVersions: cloneScriptVersions(r.scriptVersions),
	}
	r.mu.Unlock()

	var runnerWait sync.WaitGroup
	retryTicker := time.NewTicker(time.Second)
	defer retryTicker.Stop()
	apply := func(snapshot configurationSnapshot) {
		r.store.ConfigureChannels(snapshot.channels, snapshot.devices)
		active := activeChannelConfigs(snapshot.channels, snapshot.devices, snapshot.scriptVersions)
		activeIDs := make(map[int64]struct{}, len(active))
		for channelID, config := range active {
			activeIDs[channelID] = struct{}{}
			r.runnersMu.RLock()
			existing := r.runners[channelID]
			r.runnersMu.RUnlock()
			if existing != nil {
				existing.Update(config)
				continue
			}
			runner := newChannelRunner(r, config)
			r.runnersMu.Lock()
			// A refresh cannot be applied concurrently by this coordinator, but
			// keep the check here so a command submitter never sees a replaced
			// runner as an orphan.
			if currentRunner := r.runners[channelID]; currentRunner != nil {
				r.runnersMu.Unlock()
				currentRunner.Update(config)
				continue
			}
			r.runners[channelID] = runner
			r.runnersMu.Unlock()
			runnerWait.Add(1)
			go func(runner *channelRunner) {
				defer runnerWait.Done()
				runner.Run(ctx)
			}(runner)
		}
		r.runnersMu.Lock()
		for channelID, runner := range r.runners {
			if _, ok := activeIDs[channelID]; !ok {
				runner.Stop()
				delete(r.runners, channelID)
			}
		}
		r.runnersMu.Unlock()
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
			r.runnersMu.Lock()
			for channelID, runner := range r.runners {
				runner.Stop()
				delete(r.runners, channelID)
			}
			r.runnersMu.Unlock()
			runnerWait.Wait()
			return ctx.Err()
		}
	}
}

type channelRunner struct {
	runtime      *Runtime
	updates      chan channelConfig
	stop         chan struct{}
	commandQueue *commandQueue
	commandWake  chan struct{}
}

func newChannelRunner(runtime *Runtime, initial channelConfig) *channelRunner {
	runner := &channelRunner{
		runtime:      runtime,
		updates:      make(chan channelConfig, 1),
		stop:         make(chan struct{}),
		commandQueue: newCommandQueue(runtime.commandQueueCapacityValue()),
		commandWake:  make(chan struct{}, 1),
	}
	runner.Update(initial)
	return runner
}

func (r *channelRunner) SetCommandQueueCapacity(capacity int) {
	r.commandQueue.SetCapacity(capacity)
}

func (r *channelRunner) EnqueueCommand(command queuedCommand) error {
	select {
	case <-r.stop:
		return ErrCommandRuntimeStopped
	default:
	}
	if err := r.commandQueue.Enqueue(command); err != nil {
		return err
	}
	select {
	case r.commandWake <- struct{}{}:
	default:
	}
	return nil
}

func (r *channelRunner) executeCommand(ctx context.Context, config channelConfig, device Device, session *pacedSession, reads []RegisterBlockRead, command queuedCommand) {
	cycle, err := r.runtime.captureScriptCycle(config.channel, device, config.scriptVersions)
	if err == nil && (!cycle.enabled || cycle.executor == nil) {
		err = ErrCommandUnavailable
	}
	executor, ok := cycle.executor.(ScriptCommandExecutor)
	if err == nil && !ok {
		err = ErrCommandUnavailable
	}
	var result script.Result
	if err == nil {
		if command.request.OnStarted != nil {
			err = command.request.OnStarted()
		}
	}
	if err == nil {
		host := newDeviceScriptHost(session, device.UnitID, reads)
		result, err = executor.ExecuteCommand(ctx, cycle.version, cycle.invocation, host, command.request.Name, command.request.Args)
	}
	if err == nil {
		runtimeConfig := r.runtime.scriptConfig()
		if runtimeConfig.EventSink != nil && len(result.Events) > 0 {
			runtimeConfig.EventSink(ctx, device, fromScriptVersion(cycle.version), result.Events)
		}
	}
	resolveCommandResult(command.done, CommandExecutionResult{Version: cycle.version, Result: result, Err: err})
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
	pollsSinceCommand := 0
	var sharedSession *pacedSession
	deviceSessions := make(map[int64]*pacedSession)
	var channelPacer *requestPacer
	scriptIdentities := make(map[int64]ScriptStateIdentity)
	resetScriptStateForDevice := func(deviceID int64) {
		identity, ok := scriptIdentities[deviceID]
		if !ok {
			return
		}
		r.runtime.resetScriptState(identity)
		delete(scriptIdentities, deviceID)
	}
	resetAllScriptStates := func() {
		for deviceID := range scriptIdentities {
			resetScriptStateForDevice(deviceID)
		}
	}
	removeCurrentStates := func() {
		for _, device := range current.devices {
			if !r.runtime.store.IsConfigured(device.ID) {
				r.runtime.store.Remove(device.ID)
			}
		}
	}

	closeSharedSession := func() {
		if sharedSession == nil {
			return
		}
		if err := sharedSession.Close(); err != nil {
			r.runtime.logger.Printf("关闭采集通道会话失败 channel_id=%d error=%v", current.channel.ID, err)
		}
		sharedSession = nil
	}
	closeDeviceSession := func(deviceID int64) {
		session := deviceSessions[deviceID]
		if session == nil {
			return
		}
		if err := session.Close(); err != nil {
			r.runtime.logger.Printf("关闭设备会话失败 channel_id=%d device_id=%d error=%v", current.channel.ID, deviceID, err)
		}
		delete(deviceSessions, deviceID)
	}
	closeAllSessions := func() {
		closeSharedSession()
		for deviceID := range deviceSessions {
			closeDeviceSession(deviceID)
		}
	}
	shutdown := func() {
		for _, command := range r.commandQueue.Drain() {
			resolveCommandResult(command.done, CommandExecutionResult{Err: ErrCommandRuntimeStopped})
		}
		removeCurrentStates()
		resetAllScriptStates()
		closeAllSessions()
	}

	apply := func(next channelConfig) {
		physicalChanged := current.active && (!next.active || !samePhysicalChannel(current.channel, next.channel))
		if physicalChanged {
			closeAllSessions()
			channelPacer = nil
		}

		oldDevices := make(map[int64]Device, len(current.devices))
		for _, device := range current.devices {
			oldDevices[device.ID] = device
		}
		newDevices := make(map[int64]Device, len(next.devices))
		for _, device := range next.devices {
			newDevices[device.ID] = device
			if oldDevice, exists := oldDevices[device.ID]; exists && !sameScriptDeviceIdentity(oldDevice, device, current.channel, next.channel) {
				resetScriptStateForDevice(device.ID)
			}
			r.runtime.store.Ensure(device)
			if _, exists := oldDevices[device.ID]; !exists {
				nextDue[device.ID] = time.Time{}
			}
		}
		for deviceID := range oldDevices {
			if _, exists := newDevices[deviceID]; !exists {
				for {
					command, queued := r.commandQueue.DequeueForDevice(deviceID)
					if !queued {
						break
					}
					resolveCommandResult(command.done, CommandExecutionResult{Err: ErrCommandTargetUnavailable})
				}
				resetScriptStateForDevice(deviceID)
				delete(nextDue, deviceID)
				closeDeviceSession(deviceID)
				if !r.runtime.store.IsConfigured(deviceID) {
					r.runtime.store.Remove(deviceID)
				}
			}
		}
		if isNetworkChannel(next.channel) {
			for deviceID, oldDevice := range oldDevices {
				newDevice, exists := newDevices[deviceID]
				if exists && !sameDeviceSession(oldDevice, newDevice, current.channel, next.channel) {
					closeDeviceSession(deviceID)
					r.runtime.store.Invalidate(newDevice)
				}
			}
		}
		for deviceID := range nextDue {
			if _, exists := newDevices[deviceID]; !exists {
				delete(nextDue, deviceID)
			}
		}

		if !next.active {
			resetAllScriptStates()
			closeAllSessions()
			for deviceID := range oldDevices {
				if !r.runtime.store.IsConfigured(deviceID) {
					r.runtime.store.Remove(deviceID)
				}
			}
			nextDue = make(map[int64]time.Time)
		}
		if channelPacer == nil {
			channelPacer = newRequestPacer(time.Duration(next.channel.InterRequestDelayMS) * time.Millisecond)
		} else {
			channelPacer.SetDelay(time.Duration(next.channel.InterRequestDelayMS) * time.Millisecond)
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
			shutdown()
			return
		case <-r.stop:
			shutdown()
			return
		default:
		}

		if config, ok := takeLatestConfig(r.updates); ok {
			apply(config)
		}
		if !current.active || len(current.devices) == 0 {
			result := waitForRunnerEvent(ctx, r.stop, r.updates, r.commandWake, 0)
			if result.stopped {
				shutdown()
				return
			}
			if result.updated {
				apply(result.config)
			}
			continue
		}

		device, due, waitFor := nextDevice(current.devices, nextDue, time.Now())
		if !due {
			result := waitForRunnerEvent(ctx, r.stop, r.updates, r.commandWake, waitFor)
			if result.stopped {
				shutdown()
				return
			}
			if result.updated {
				apply(result.config)
			} else if result.woken {
				// A command arriving while the runner is between cycles wakes
				// the runner into one immediate complete poll. The command is
				// still executed only after that poll reaches this boundary.
				for _, candidate := range current.devices {
					nextDue[candidate.ID] = time.Time{}
					break
				}
			}
			continue
		}

		var session *pacedSession
		if isNetworkChannel(current.channel) {
			session = deviceSessions[device.ID]
			if session == nil {
				raw, err := r.runtime.factory(current.channel, device)
				if err == nil && raw == nil {
					err = fmt.Errorf("Modbus 会话工厂返回空会话")
				}
				if err == nil {
					session = newPacedSessionWithPacer(raw, channelPacer)
					err = session.Open()
				}
				if err != nil {
					if session != nil {
						_ = session.Close()
					}
					delete(deviceSessions, device.ID)
					r.runtime.recordDeviceFailure(device, err)
					nextDue[device.ID] = nextDeviceDue(device)
					continue
				}
				deviceSessions[device.ID] = session
			}
		} else {
			session = sharedSession
			if session == nil {
				raw, err := r.runtime.factory(current.channel, device)
				if err == nil && raw == nil {
					err = fmt.Errorf("Modbus 会话工厂返回空会话")
				}
				if err != nil {
					r.runtime.recordChannelFailure(current.devices, err)
					nextDue[device.ID] = nextDeviceDue(device)
					continue
				}
				session = newPacedSessionWithPacer(raw, channelPacer)
				if err := session.Open(); err != nil {
					r.runtime.recordChannelFailure(current.devices, err)
					_ = session.Close()
					sharedSession = nil
					nextDue[device.ID] = nextDeviceDue(device)
					continue
				}
				sharedSession = session
			}
		}

		cycle, versionErr := r.runtime.captureScriptCycle(current.channel, device, current.scriptVersions)
		if versionErr != nil {
			r.runtime.logger.Printf("读取脚本发布版本快照失败 device_id=%d error=%v", device.ID, versionErr)
		} else if cycle.enabled {
			if previous, exists := scriptIdentities[device.ID]; exists && !sameScriptStateIdentity(previous, cycle.identity) {
				r.runtime.resetScriptState(previous)
			}
			scriptIdentities[device.ID] = cycle.identity
		} else {
			resetScriptStateForDevice(device.ID)
		}

		cycleResult := r.runtime.executeDeviceCycle(ctx, current.channel, device, session, isNetworkChannel(current.channel), cycle)
		r.runtime.recordScriptRuntimeState(device, cycle, cycleResult, time.Now().UTC())
		if cycleResult.scriptErr == nil && cycleResult.scriptExecuted {
			runtimeConfig := r.runtime.scriptConfig()
			if runtimeConfig.EventSink != nil && len(cycleResult.scriptResult.Events) > 0 {
				runtimeConfig.EventSink(ctx, device, fromScriptVersion(cycle.version), cycleResult.scriptResult.Events)
			}
		}
		r.runtime.notifyStateCycle(ctx, device)
		if cycleResult.scriptErr != nil {
			r.runtime.logger.Printf("脚本 after_poll 执行失败 device_id=%d script_version_id=%d error=%v", device.ID, cycle.version.VersionID, cycleResult.scriptErr)
		}
		if isNetworkChannel(current.channel) && ((cycleResult.staticErr != nil && !isModbusExceptionError(cycleResult.staticErr)) || cycleResult.scriptTransportError) {
			closeDeviceSession(device.ID)
		}
		interval := time.Duration(device.PollIntervalMS) * time.Millisecond
		if interval <= 0 {
			interval = time.Second
		}
		// The next period starts after the complete device read cycle.
		nextDue[device.ID] = time.Now().Add(interval)
		pollsSinceCommand++
		if pollsSinceCommand >= r.runtime.commandPollFairnessValue() {
			if command, queued := r.commandQueue.DequeueForDevice(device.ID); queued {
				r.executeCommand(ctx, current, device, session, cycleResult.staticReads, command)
				pollsSinceCommand = 0
			}
		}
	}
}

func isNetworkChannel(channel Channel) bool {
	return channel.Protocol == ProtocolModbusTCP || channel.Protocol == ProtocolModbusUDP || channel.Protocol == ProtocolModbusRTUOverUDP
}

func sameDeviceSession(left, right Device, leftChannel, rightChannel Channel) bool {
	if left.UnitID != right.UnitID || left.ChannelID != right.ChannelID || leftChannel.Protocol != rightChannel.Protocol || leftChannel.TimeoutMS != rightChannel.TimeoutMS {
		return false
	}
	if left.NetworkEndpoint == nil || right.NetworkEndpoint == nil {
		return left.NetworkEndpoint == nil && right.NetworkEndpoint == nil
	}
	return *left.NetworkEndpoint == *right.NetworkEndpoint
}

func nextDeviceDue(device Device) time.Time {
	interval := time.Duration(device.PollIntervalMS) * time.Millisecond
	if interval <= 0 {
		interval = time.Second
	}
	return time.Now().Add(interval)
}

type runnerWaitResult struct {
	config  channelConfig
	updated bool
	stopped bool
	woken   bool
}

func waitForRunnerEvent(ctx context.Context, stop <-chan struct{}, updates <-chan channelConfig, wakes <-chan struct{}, duration time.Duration) runnerWaitResult {
	if duration <= 0 {
		select {
		case <-ctx.Done():
			return runnerWaitResult{stopped: true}
		case <-stop:
			return runnerWaitResult{stopped: true}
		case config := <-updates:
			return runnerWaitResult{config: config, updated: true}
		case <-wakes:
			return runnerWaitResult{woken: true}
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
	case <-wakes:
		return runnerWaitResult{woken: true}
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
		r.notifyStateCycle(context.Background(), device)
	}
	r.logger.Printf("采集通道不可用 error=%v", err)
}

func (r *Runtime) recordDeviceFailure(device Device, err error) {
	reads := make([]RegisterBlockRead, len(device.RegisterBlocks))
	for index, block := range device.RegisterBlocks {
		reads[index] = RegisterBlockRead{Block: block, Err: err}
	}
	r.store.RecordCycle(device, reads, time.Now().UTC())
	r.notifyStateCycle(context.Background(), device)
	r.logger.Printf("采集设备不可用 device_id=%d error=%v", device.ID, err)
}

func (r *Runtime) notifyStateCycle(ctx context.Context, device Device) {
	config := r.scriptConfig()
	if config.CycleSink == nil {
		return
	}
	state, ok := r.store.Get(device.ID)
	if !ok {
		return
	}
	config.CycleSink(ctx, state)
}

func PollChannelOnce(ctx context.Context, channel Channel, devices []Device, session ModbusSession, store *CurrentStateStore) error {
	if session == nil || store == nil {
		return fmt.Errorf("采集通道依赖不能为空")
	}
	for _, device := range devices {
		if device.Enabled != Enabled || device.ChannelID != channel.ID {
			continue
		}
		_ = pollDeviceCycle(ctx, channel, device, session, store, false)
	}
	return nil
}

func pollDeviceCycle(ctx context.Context, _ Channel, device Device, session ModbusSession, store *CurrentStateStore, stopOnError bool) error {
	return pollDeviceCycleWithReads(ctx, device, session, store, stopOnError, nil)
}

func pollDeviceCycleWithReads(ctx context.Context, device Device, session ModbusSession, store *CurrentStateStore, stopOnError bool, capturedReads *[]RegisterBlockRead) error {
	reads := make([]RegisterBlockRead, 0, len(device.RegisterBlocks))
	var setupErr error
	if err := session.SetUnitID(device.UnitID); err != nil {
		setupErr = fmt.Errorf("设置 Modbus 地址 %d 失败: %w", device.UnitID, err)
	}
	for _, block := range device.RegisterBlocks {
		read := RegisterBlockRead{Block: block}
		if setupErr != nil {
			read.Err = setupErr
		} else {
			read.Values, read.Err = readRegisterBlock(ctx, session, device.UnitID, block)
		}
		reads = append(reads, read)
		if stopOnError && read.Err != nil && !isModbusExceptionError(read.Err) {
			break
		}
	}
	store.RecordCycle(device, reads, time.Now().UTC())
	if capturedReads != nil {
		*capturedReads = append((*capturedReads)[:0], reads...)
	}
	if setupErr != nil {
		return setupErr
	}
	for _, read := range reads {
		if read.Err != nil {
			return read.Err
		}
	}
	return nil
}

func activeChannelConfigs(channels []Channel, devices []Device, scriptVersionSnapshots ...map[int64]ScriptVersion) map[int64]channelConfig {
	var scriptVersions map[int64]ScriptVersion
	if len(scriptVersionSnapshots) > 0 {
		scriptVersions = scriptVersionSnapshots[0]
	}
	result := make(map[int64]channelConfig)
	for _, channel := range channels {
		if channel.Enabled != Enabled {
			continue
		}
		channelDevices := devicesForChannel(devices, channel.ID)
		if len(channelDevices) == 0 {
			continue
		}
		result[channel.ID] = channelConfig{
			active:         true,
			channel:        channel,
			devices:        channelDevices,
			scriptVersions: scriptVersionsForDevices(scriptVersions, channelDevices),
		}
	}
	return result
}

func devicesForChannel(devices []Device, channelID int64) []Device {
	result := make([]Device, 0)
	for _, device := range devices {
		if device.ChannelID == channelID && device.Enabled == Enabled {
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
	if left.Protocol != right.Protocol || left.TimeoutMS != right.TimeoutMS {
		return false
	}
	if left.Protocol != ProtocolModbusRTU {
		return true
	}
	if left.SerialConfig == nil || right.SerialConfig == nil {
		return left.SerialConfig == nil && right.SerialConfig == nil
	}
	return *left.SerialConfig == *right.SerialConfig
}

func cloneChannels(channels []Channel) []Channel {
	result := append([]Channel(nil), channels...)
	for index := range result {
		if result[index].SerialConfig != nil {
			serial := *result[index].SerialConfig
			result[index].SerialConfig = &serial
		}
	}
	return result
}

func cloneDevices(devices []Device) []Device {
	result := append([]Device(nil), devices...)
	for index := range result {
		if result[index].ScriptID != nil {
			scriptID := *result[index].ScriptID
			result[index].ScriptID = &scriptID
		}
		result[index].RegisterBlocks = append([]RegisterBlock(nil), result[index].RegisterBlocks...)
		if result[index].NetworkEndpoint != nil {
			endpoint := *result[index].NetworkEndpoint
			result[index].NetworkEndpoint = &endpoint
		}
	}
	return result
}

func cloneChannelConfig(config channelConfig) channelConfig {
	config.devices = cloneDevices(config.devices)
	config.scriptVersions = cloneScriptVersions(config.scriptVersions)
	return config
}

func cloneScriptVersions(versions map[int64]ScriptVersion) map[int64]ScriptVersion {
	if len(versions) == 0 {
		return nil
	}
	result := make(map[int64]ScriptVersion, len(versions))
	for deviceID, version := range versions {
		result[deviceID] = version
	}
	return result
}

func scriptVersionsForDevices(versions map[int64]ScriptVersion, devices []Device) map[int64]ScriptVersion {
	if len(versions) == 0 {
		return nil
	}
	result := make(map[int64]ScriptVersion)
	for _, device := range devices {
		if version, ok := versions[device.ID]; ok {
			result[device.ID] = version
		}
	}
	return result
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
