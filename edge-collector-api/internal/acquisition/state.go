package acquisition

import (
	"sort"
	"strconv"
	"sync"
	"time"
)

type CommunicationStatus string

const (
	StatusInitial  CommunicationStatus = "INITIAL"
	StatusOnline   CommunicationStatus = "ONLINE"
	StatusDegraded CommunicationStatus = "DEGRADED"
	StatusOffline  CommunicationStatus = "OFFLINE"
)

type ChannelRuntimeStatus string

const (
	ChannelStatusIdle     ChannelRuntimeStatus = "IDLE"
	ChannelStatusStarting ChannelRuntimeStatus = "STARTING"
	ChannelStatusOnline   ChannelRuntimeStatus = "ONLINE"
	ChannelStatusDegraded ChannelRuntimeStatus = "DEGRADED"
	ChannelStatusOffline  ChannelRuntimeStatus = "OFFLINE"
)

type ChannelRuntimeState struct {
	ChannelID     int64                `json:"channelId"`
	ChannelName   string               `json:"channelName"`
	Protocol      string               `json:"protocol"`
	Status        ChannelRuntimeStatus `json:"status"`
	LastAttemptAt *time.Time           `json:"lastAttemptAt"`
	LastSuccessAt *time.Time           `json:"lastSuccessAt"`
	LastError     string               `json:"lastError,omitempty"`
}

type RegisterBlockState struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	FunctionCode  int        `json:"functionCode"`
	StartAddress  int        `json:"startAddress"`
	Quantity      int        `json:"quantity"`
	SortOrder     int        `json:"sortOrder"`
	Values        []*uint16  `json:"values"`
	Valid         bool       `json:"valid"`
	LastAttemptAt *time.Time `json:"lastAttemptAt"`
	LastSuccessAt *time.Time `json:"lastSuccessAt"`
	LastError     string     `json:"lastError,omitempty"`
}

type RegisterBlockRead struct {
	Block  RegisterBlock
	Values []uint16
	Err    error
}

type CurrentState struct {
	DeviceID            int64                `json:"deviceId"`
	DeviceName          string               `json:"deviceName"`
	ChannelID           int64                `json:"channelId"`
	UnitID              uint8                `json:"unitId"`
	NetworkEndpoint     *NetworkEndpoint     `json:"networkEndpoint,omitempty"`
	RegisterBlocks      []RegisterBlockState `json:"registerBlocks"`
	LastAttemptAt       *time.Time           `json:"lastAttemptAt"`
	LastSuccessAt       *time.Time           `json:"lastSuccessAt"`
	Status              CommunicationStatus  `json:"status"`
	ConsecutiveFailures int                  `json:"consecutiveFailures"`
	LastError           string               `json:"lastError,omitempty"`
}

type CurrentStateStore struct {
	mu                sync.RWMutex
	states            map[int64]CurrentState
	channelStates     map[int64]ChannelRuntimeState
	channelDevices    map[int64]map[int64]struct{}
	configuredDevices map[int64]configuredDevice
}

type configuredDevice struct {
	device Device
	active bool
}

func NewCurrentStateStore() *CurrentStateStore {
	return &CurrentStateStore{
		states:            make(map[int64]CurrentState),
		channelStates:     make(map[int64]ChannelRuntimeState),
		channelDevices:    make(map[int64]map[int64]struct{}),
		configuredDevices: make(map[int64]configuredDevice),
	}
}

// ConfigureChannels records the enabled-device membership used to aggregate
// channel status. It does not clear device snapshots during a configuration
// refresh; runners remove snapshots only when the new configuration is active.
func (s *CurrentStateStore) ConfigureChannels(channels []Channel, devices []Device) {
	s.mu.Lock()
	defer s.mu.Unlock()

	members := make(map[int64]map[int64]struct{})
	configured := make(map[int64]configuredDevice, len(devices))
	for _, device := range devices {
		configured[device.ID] = configuredDevice{
			device: cloneDevices([]Device{device})[0],
			active: device.Enabled == Enabled,
		}
		if device.Enabled != Enabled {
			continue
		}
		if members[device.ChannelID] == nil {
			members[device.ChannelID] = make(map[int64]struct{})
		}
		members[device.ChannelID][device.ID] = struct{}{}
	}
	// Keep tombstones for removed devices so an in-flight old runner cannot
	// recreate a snapshot after the new configuration has taken ownership.
	for deviceID, previous := range s.configuredDevices {
		if _, ok := configured[deviceID]; !ok {
			previous.active = false
			configured[deviceID] = previous
		}
	}
	s.configuredDevices = configured
	s.channelDevices = members
	for _, channel := range channels {
		state := s.channelStates[channel.ID]
		state.ChannelID = channel.ID
		state.ChannelName = channel.Name
		state.Protocol = channel.Protocol
		s.channelStates[channel.ID] = state
		s.updateChannelStateLocked(channel.ID)
	}
	for channelID := range s.channelStates {
		if _, ok := members[channelID]; !ok {
			s.updateChannelStateLocked(channelID)
		}
	}
}

func (s *CurrentStateStore) Ensure(device Device) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.states[device.ID]
	if !ok {
		s.states[device.ID] = newCurrentState(device)
		return
	}
	state.DeviceName = device.Name
	identityChanged := state.ChannelID != device.ChannelID || state.UnitID != device.UnitID || !sameNetworkEndpoint(state.NetworkEndpoint, device.NetworkEndpoint)
	state.ChannelID = device.ChannelID
	state.UnitID = device.UnitID
	state.NetworkEndpoint = cloneNetworkEndpoint(device.NetworkEndpoint)
	var changed bool
	state.RegisterBlocks, changed = reconcileBlockStates(state.RegisterBlocks, device.RegisterBlocks)
	if changed {
		state.Status = StatusInitial
		state.ConsecutiveFailures = 0
		state.LastAttemptAt = nil
		state.LastSuccessAt = nil
		state.LastError = ""
	}
	if identityChanged {
		for index := range state.RegisterBlocks {
			state.RegisterBlocks[index].Valid = false
		}
		state.Status = StatusInitial
		state.ConsecutiveFailures = 0
		state.LastError = "等待新配置首次成功"
	}
	s.states[device.ID] = state
	s.updateChannelStateLocked(device.ChannelID)
}

func (s *CurrentStateStore) RecordCycle(device Device, reads []RegisterBlockRead, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if configured, ok := s.configuredDevices[device.ID]; ok &&
		(!configured.active || !sameConfiguredDevice(configured, device)) {
		return
	}

	state, ok := s.states[device.ID]
	if !ok {
		state = newCurrentState(device)
	}
	state.DeviceName = device.Name
	state.ChannelID = device.ChannelID
	state.UnitID = device.UnitID
	state.NetworkEndpoint = cloneNetworkEndpoint(device.NetworkEndpoint)
	var shapeChanged bool
	state.RegisterBlocks, shapeChanged = reconcileBlockStates(state.RegisterBlocks, device.RegisterBlocks)
	if shapeChanged {
		state.LastSuccessAt = nil
		state.ConsecutiveFailures = 0
		state.LastError = ""
	}
	state.LastAttemptAt = timePtr(at)

	byBlock := make(map[string]RegisterBlockRead, len(reads))
	for _, read := range reads {
		byBlock[blockIdentity(read.Block)] = read
	}
	successes := 0
	var firstErr error
	for index := range state.RegisterBlocks {
		blockState := &state.RegisterBlocks[index]
		read, found := byBlock[blockIdentity(registerBlockFromState(*blockState))]
		if !found {
			blockState.Valid = false
			blockState.LastAttemptAt = timePtr(at)
			if firstErr == nil {
				firstErr = errorString("读取块未执行")
			}
			continue
		}
		blockState.LastAttemptAt = timePtr(at)
		if read.Err != nil {
			blockState.Valid = false
			blockState.LastError = read.Err.Error()
			if firstErr == nil {
				firstErr = read.Err
			}
			continue
		}
		if len(read.Values) != blockState.Quantity {
			blockState.Valid = false
			blockState.LastError = "读取块响应数量不符"
			if firstErr == nil {
				firstErr = errorString(blockState.LastError)
			}
			continue
		}
		blockState.Values = uint16Pointers(read.Values)
		blockState.Valid = true
		blockState.LastSuccessAt = timePtr(at)
		blockState.LastError = ""
		successes++
	}

	total := len(state.RegisterBlocks)
	switch {
	case total == 0:
		state.Status = StatusInitial
		state.ConsecutiveFailures = 0
	case successes == total:
		state.Status = StatusOnline
		state.ConsecutiveFailures = 0
		state.LastSuccessAt = timePtr(at)
		state.LastError = ""
	case successes > 0:
		state.Status = StatusDegraded
		state.ConsecutiveFailures = 0
		state.LastSuccessAt = timePtr(at)
		state.LastError = firstError(firstErr)
	default:
		state.ConsecutiveFailures++
		state.LastError = firstError(firstErr)
		threshold := device.FailureThreshold
		if threshold < 1 {
			threshold = 3
		}
		if state.ConsecutiveFailures >= threshold {
			state.Status = StatusOffline
		} else {
			state.Status = StatusDegraded
		}
	}
	s.states[device.ID] = state
	s.updateChannelStateLocked(device.ChannelID)
}

func sameConfiguredDevice(configured configuredDevice, device Device) bool {
	if configured.device.ID != device.ID || configured.device.Name != device.Name ||
		configured.device.DeviceType != device.DeviceType || configured.device.ChannelID != device.ChannelID ||
		configured.device.UnitID != device.UnitID || configured.device.PollIntervalMS != device.PollIntervalMS ||
		configured.device.FailureThreshold != device.FailureThreshold || configured.device.Enabled != device.Enabled {
		return false
	}
	return sameNetworkEndpoint(configured.device.NetworkEndpoint, device.NetworkEndpoint) &&
		sameRegisterBlocks(configured.device.RegisterBlocks, device.RegisterBlocks)
}

func sameRegisterBlocks(left, right []RegisterBlock) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// Invalidate keeps the last successful register values for inspection while
// making them ineligible for the current configuration identity.
func (s *CurrentStateStore) Invalidate(device Device) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.states[device.ID]
	if !ok {
		state = newCurrentState(device)
	}
	state.DeviceName = device.Name
	state.ChannelID = device.ChannelID
	state.UnitID = device.UnitID
	state.NetworkEndpoint = cloneNetworkEndpoint(device.NetworkEndpoint)
	state.RegisterBlocks, _ = reconcileBlockStates(state.RegisterBlocks, device.RegisterBlocks)
	for index := range state.RegisterBlocks {
		state.RegisterBlocks[index].Valid = false
	}
	state.Status = StatusInitial
	state.ConsecutiveFailures = 0
	state.LastError = "等待新配置首次成功"
	s.states[device.ID] = state
	s.updateChannelStateLocked(device.ChannelID)
}

func (s *CurrentStateStore) Get(deviceID int64) (CurrentState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.states[deviceID]
	if !ok {
		return CurrentState{}, false
	}
	return cloneState(state), true
}

func (s *CurrentStateStore) Remove(deviceID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channelID := s.states[deviceID].ChannelID
	delete(s.states, deviceID)
	s.updateChannelStateLocked(channelID)
}

func (s *CurrentStateStore) IsConfigured(deviceID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, members := range s.channelDevices {
		if _, ok := members[deviceID]; ok {
			return true
		}
	}
	return false
}

func (s *CurrentStateStore) List() []CurrentState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	states := make([]CurrentState, 0, len(s.states))
	for _, state := range s.states {
		states = append(states, cloneState(state))
	}
	sort.Slice(states, func(i, j int) bool { return states[i].DeviceID < states[j].DeviceID })
	return states
}

func (s *CurrentStateStore) ChannelStates() []ChannelRuntimeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	states := make([]ChannelRuntimeState, 0, len(s.channelStates))
	for _, state := range s.channelStates {
		states = append(states, cloneChannelRuntimeState(state))
	}
	sort.Slice(states, func(i, j int) bool { return states[i].ChannelID < states[j].ChannelID })
	return states
}

func (s *CurrentStateStore) ChannelState(channelID int64) (ChannelRuntimeState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.channelStates[channelID]
	if !ok {
		return ChannelRuntimeState{}, false
	}
	return cloneChannelRuntimeState(state), true
}

func (s *CurrentStateStore) updateChannelStateLocked(channelID int64) {
	state := s.channelStates[channelID]
	members := s.channelDevices[channelID]
	if len(members) == 0 {
		state.Status = ChannelStatusIdle
		state.LastAttemptAt = nil
		state.LastSuccessAt = nil
		state.LastError = ""
		s.channelStates[channelID] = state
		return
	}

	allOnline := true
	allOffline := true
	hasOnline := false
	hasStarting := false
	hasDegraded := false
	state.LastError = ""
	for deviceID := range members {
		deviceState, ok := s.states[deviceID]
		if !ok || deviceState.Status == StatusInitial {
			hasStarting = true
			allOnline = false
			allOffline = false
			continue
		}
		switch deviceState.Status {
		case StatusOnline:
			hasOnline = true
			allOffline = false
		case StatusOffline:
			allOnline = false
		default:
			allOnline = false
			allOffline = false
			if deviceState.Status == StatusDegraded {
				hasDegraded = true
			}
		}
		if deviceState.LastAttemptAt != nil && (state.LastAttemptAt == nil || deviceState.LastAttemptAt.After(*state.LastAttemptAt)) {
			state.LastAttemptAt = cloneTimePointer(deviceState.LastAttemptAt)
		}
		if deviceState.LastSuccessAt != nil && (state.LastSuccessAt == nil || deviceState.LastSuccessAt.After(*state.LastSuccessAt)) {
			state.LastSuccessAt = cloneTimePointer(deviceState.LastSuccessAt)
		}
		if deviceState.LastError != "" {
			state.LastError = deviceState.LastError
		}
	}
	switch {
	case allOnline:
		state.Status = ChannelStatusOnline
	case hasOnline:
		state.Status = ChannelStatusDegraded
	case hasStarting:
		state.Status = ChannelStatusStarting
	case allOffline:
		state.Status = ChannelStatusOffline
	case hasDegraded:
		state.Status = ChannelStatusDegraded
	default:
		state.Status = ChannelStatusStarting
	}
	s.channelStates[channelID] = state
}

func newCurrentState(device Device) CurrentState {
	blocks, _ := reconcileBlockStates(nil, device.RegisterBlocks)
	return CurrentState{
		DeviceID:        device.ID,
		DeviceName:      device.Name,
		ChannelID:       device.ChannelID,
		UnitID:          device.UnitID,
		NetworkEndpoint: cloneNetworkEndpoint(device.NetworkEndpoint),
		RegisterBlocks:  blocks,
		Status:          StatusInitial,
	}
}

func reconcileBlockStates(existing []RegisterBlockState, configured []RegisterBlock) ([]RegisterBlockState, bool) {
	byIdentity := make(map[string]RegisterBlockState, len(existing))
	for _, state := range existing {
		byIdentity[blockStateIdentity(state)] = state
	}
	result := make([]RegisterBlockState, 0, len(configured))
	changed := len(existing) != len(configured)
	for _, block := range configured {
		state, found := byIdentity[blockIdentity(block)]
		if !found {
			state = newRegisterBlockState(block)
			changed = true
		} else if !sameRegisterShape(registerBlockFromState(state), block) {
			state = newRegisterBlockState(block)
			changed = true
		} else {
			state.ID = block.ID
			state.Name = block.Name
			state.FunctionCode = block.FunctionCode
			state.StartAddress = block.StartAddress
			state.Quantity = block.Quantity
			state.SortOrder = block.SortOrder
		}
		result = append(result, state)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].SortOrder != result[j].SortOrder {
			return result[i].SortOrder < result[j].SortOrder
		}
		return result[i].ID < result[j].ID
	})
	return result, changed
}

func newRegisterBlockState(block RegisterBlock) RegisterBlockState {
	quantity := block.Quantity
	if quantity < 0 {
		quantity = 0
	}
	return RegisterBlockState{
		ID:           block.ID,
		Name:         block.Name,
		FunctionCode: block.FunctionCode,
		StartAddress: block.StartAddress,
		Quantity:     block.Quantity,
		SortOrder:    block.SortOrder,
		Values:       make([]*uint16, quantity),
	}
}

func registerBlockFromState(state RegisterBlockState) RegisterBlock {
	return RegisterBlock{ID: state.ID, Name: state.Name, FunctionCode: state.FunctionCode, StartAddress: state.StartAddress, Quantity: state.Quantity, SortOrder: state.SortOrder}
}

func sameRegisterShape(left, right RegisterBlock) bool {
	return left.FunctionCode == right.FunctionCode && left.StartAddress == right.StartAddress && left.Quantity == right.Quantity
}

func blockIdentity(block RegisterBlock) string {
	if block.ID > 0 {
		return "id:" + formatInt64(block.ID)
	}
	return "shape:" + block.Name + ":" + formatInt(block.FunctionCode) + ":" + formatInt(block.StartAddress) + ":" + formatInt(block.Quantity)
}

func blockStateIdentity(state RegisterBlockState) string {
	return blockIdentity(registerBlockFromState(state))
}

func uint16Pointers(values []uint16) []*uint16 {
	result := make([]*uint16, len(values))
	for index, value := range values {
		copy := value
		result[index] = &copy
	}
	return result
}

func cloneState(state CurrentState) CurrentState {
	state.NetworkEndpoint = cloneNetworkEndpoint(state.NetworkEndpoint)
	if state.RegisterBlocks == nil {
		state.RegisterBlocks = []RegisterBlockState{}
	} else {
		state.RegisterBlocks = append([]RegisterBlockState(nil), state.RegisterBlocks...)
	}
	for index := range state.RegisterBlocks {
		state.RegisterBlocks[index].Values = append([]*uint16(nil), state.RegisterBlocks[index].Values...)
		for valueIndex, value := range state.RegisterBlocks[index].Values {
			if value == nil {
				continue
			}
			copy := *value
			state.RegisterBlocks[index].Values[valueIndex] = &copy
		}
		state.RegisterBlocks[index].LastAttemptAt = cloneTimePointer(state.RegisterBlocks[index].LastAttemptAt)
		state.RegisterBlocks[index].LastSuccessAt = cloneTimePointer(state.RegisterBlocks[index].LastSuccessAt)
	}
	state.LastAttemptAt = cloneTimePointer(state.LastAttemptAt)
	state.LastSuccessAt = cloneTimePointer(state.LastSuccessAt)
	return state
}

func cloneChannelRuntimeState(state ChannelRuntimeState) ChannelRuntimeState {
	state.LastAttemptAt = cloneTimePointer(state.LastAttemptAt)
	state.LastSuccessAt = cloneTimePointer(state.LastSuccessAt)
	return state
}

func sameNetworkEndpoint(left, right *NetworkEndpoint) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneNetworkEndpoint(endpoint *NetworkEndpoint) *NetworkEndpoint {
	if endpoint == nil {
		return nil
	}
	copy := *endpoint
	return &copy
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func firstError(err error) string {
	if err == nil {
		return "读取块失败"
	}
	return err.Error()
}

type errorString string

func (e errorString) Error() string { return string(e) }

func formatInt(value int) string     { return strconv.Itoa(value) }
func formatInt64(value int64) string { return strconv.FormatInt(value, 10) }
func timePtr(value time.Time) *time.Time {
	copy := value
	return &copy
}
