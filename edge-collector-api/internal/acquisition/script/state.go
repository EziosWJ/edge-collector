package script

import "sync"

type stateEntry struct {
	state  map[string]any
	events []Event
}

type stateStore struct {
	mu      sync.Mutex
	entries map[StateScope]stateEntry
	locks   map[StateScope]*stateScopeLock
}

type stateScopeLock struct {
	mu   sync.Mutex
	refs int
}

func newStateStore() *stateStore {
	return &stateStore{
		entries: make(map[StateScope]stateEntry),
		locks:   make(map[StateScope]*stateScopeLock),
	}
}

// lockScope serializes invocations for the same state scope while preserving
// parallelism between different devices or script versions.
func (s *stateStore) lockScope(scope StateScope) func() {
	s.mu.Lock()
	lock := s.locks[scope]
	if lock == nil {
		lock = new(stateScopeLock)
		s.locks[scope] = lock
	}
	lock.refs++
	s.mu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()

		s.mu.Lock()
		lock.refs--
		if lock.refs == 0 && s.locks[scope] == lock {
			if _, exists := s.entries[scope]; !exists {
				delete(s.locks, scope)
			}
		}
		s.mu.Unlock()
	}
}

func (s *stateStore) load(scope StateScope) stateEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[scope]
	return stateEntry{state: cloneState(entry.state), events: cloneEvents(entry.events)}
}

func (s *stateStore) commit(scope StateScope, state map[string]any, events []Event, maxEvents int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := stateEntry{state: cloneState(state), events: cloneEvents(s.entries[scope].events)}
	for _, event := range events {
		duplicate := false
		for _, existing := range entry.events {
			if existing.Kind == event.Kind && existing.Key == event.Key {
				duplicate = true
				break
			}
		}
		if !duplicate {
			entry.events = append(entry.events, cloneEvents([]Event{event})[0])
		}
	}
	if len(entry.events) > maxEvents {
		entry.events = append([]Event(nil), entry.events[len(entry.events)-maxEvents:]...)
	}
	s.entries[scope] = entry
}

func (s *stateStore) snapshot(scope StateScope) StateSnapshot {
	unlock := s.lockScope(scope)
	defer unlock()
	entry := s.load(scope)
	return StateSnapshot{State: entry.state, Events: entry.events}
}

func (s *stateStore) reset(scope StateScope) {
	unlock := s.lockScope(scope)
	defer unlock()
	s.mu.Lock()
	delete(s.entries, scope)
	s.mu.Unlock()
}
