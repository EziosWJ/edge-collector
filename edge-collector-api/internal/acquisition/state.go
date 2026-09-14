package acquisition

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var errPartialRead = errors.New("partial register read")

type CommunicationStatus string

const (
	StatusInitial  CommunicationStatus = "INITIAL"
	StatusOnline   CommunicationStatus = "ONLINE"
	StatusDegraded CommunicationStatus = "DEGRADED"
	StatusOffline  CommunicationStatus = "OFFLINE"
)

type FeedProtectorReading struct {
	Data        FeedProtectorData
	ValidFields map[string]bool
}

type CurrentState struct {
	DeviceID            int64                `json:"deviceId"`
	DeviceName          string               `json:"deviceName"`
	ChannelID           int64                `json:"channelId"`
	SlaveID             uint8                `json:"slaveId"`
	Data                FeedProtectorData    `json:"data"`
	FieldValidity       map[string]bool      `json:"fieldValidity"`
	FieldUpdatedAt      map[string]time.Time `json:"fieldUpdatedAt"`
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
	if ok {
		state.DeviceName = device.Name
		state.ChannelID = device.ChannelID
		state.SlaveID = device.SlaveID
		s.states[device.ID] = state
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

func (s *CurrentStateStore) Record(device Device, reading FeedProtectorReading, readErr error, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.states[device.ID]
	if state.DeviceID == 0 {
		state = CurrentState{
			DeviceID:       device.ID,
			FieldValidity:  make(map[string]bool),
			FieldUpdatedAt: make(map[string]time.Time),
			Status:         StatusInitial,
		}
	}
	state.DeviceName = device.Name
	state.ChannelID = device.ChannelID
	state.SlaveID = device.SlaveID
	state.LastAttemptAt = timePtr(at)

	for field, valid := range reading.ValidFields {
		state.FieldValidity[field] = valid
		if !valid {
			continue
		}
		applyField(&state.Data, reading.Data, field)
		state.FieldUpdatedAt[field] = at
	}
	if readErr != nil {
		for _, field := range feedProtectorFields {
			if !reading.ValidFields[field] {
				state.FieldValidity[field] = false
			}
		}
	}

	if readErr == nil {
		state.Status = StatusOnline
		state.ConsecutiveFailures = 0
		state.LastError = ""
		state.LastSuccessAt = timePtr(at)
	} else {
		state.ConsecutiveFailures++
		state.LastError = readErr.Error()
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

var feedProtectorFields = []string{
	"voltage", "current", "activePower", "frequency", "powerFactor", "status",
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

func applyField(target *FeedProtectorData, source FeedProtectorData, field string) {
	switch field {
	case "voltage":
		target.Voltage = source.Voltage
	case "current":
		target.Current = source.Current
	case "activePower":
		target.ActivePower = source.ActivePower
	case "frequency":
		target.Frequency = source.Frequency
	case "powerFactor":
		target.PowerFactor = source.PowerFactor
	case "status":
		target.Status = source.Status
	}
}

func cloneState(state CurrentState) CurrentState {
	state.FieldValidity = cloneBoolMap(state.FieldValidity)
	state.FieldUpdatedAt = cloneTimeMap(state.FieldUpdatedAt)
	return state
}

func cloneBoolMap(value map[string]bool) map[string]bool {
	result := make(map[string]bool, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func cloneTimeMap(value map[string]time.Time) map[string]time.Time {
	result := make(map[string]time.Time, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func timePtr(value time.Time) *time.Time {
	copy := value
	return &copy
}
