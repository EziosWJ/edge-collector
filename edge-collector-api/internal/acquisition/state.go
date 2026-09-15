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
	SlaveID             uint8                `json:"slaveId"`
	RegisterBlocks      []RegisterBlockState `json:"registerBlocks"`
	LastAttemptAt       *time.Time           `json:"lastAttemptAt"`
	LastSuccessAt       *time.Time           `json:"lastSuccessAt"`
	Status              CommunicationStatus  `json:"status"`
	ConsecutiveFailures int                  `json:"consecutiveFailures"`
	LastError           string               `json:"lastError,omitempty"`
}

type CurrentStateStore struct {
	mu     sync.RWMutex
	states map[int64]CurrentState
}

func NewCurrentStateStore() *CurrentStateStore {
	return &CurrentStateStore{states: make(map[int64]CurrentState)}
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
	state.ChannelID = device.ChannelID
	state.SlaveID = device.SlaveID
	var changed bool
	state.RegisterBlocks, changed = reconcileBlockStates(state.RegisterBlocks, device.RegisterBlocks)
	if changed {
		state.Status = StatusInitial
		state.ConsecutiveFailures = 0
		state.LastAttemptAt = nil
		state.LastSuccessAt = nil
		state.LastError = ""
	}
	s.states[device.ID] = state
}

func (s *CurrentStateStore) RecordCycle(device Device, reads []RegisterBlockRead, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.states[device.ID]
	if !ok {
		state = newCurrentState(device)
	}
	state.DeviceName = device.Name
	state.ChannelID = device.ChannelID
	state.SlaveID = device.SlaveID
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
	delete(s.states, deviceID)
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

func newCurrentState(device Device) CurrentState {
	blocks, _ := reconcileBlockStates(nil, device.RegisterBlocks)
	return CurrentState{
		DeviceID:       device.ID,
		DeviceName:     device.Name,
		ChannelID:      device.ChannelID,
		SlaveID:        device.SlaveID,
		RegisterBlocks: blocks,
		Status:         StatusInitial,
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
