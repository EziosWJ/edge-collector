package script

import "testing"

func TestStateStoreReleasesUnusedScopeLocks(t *testing.T) {
	store := newStateStore()
	scope := StateScope{DeviceID: 1, ScriptID: 2, ScriptVersionID: 3}

	unlock := store.lockScope(scope)
	unlock()
	if len(store.locks) != 0 {
		t.Fatalf("locks after an unused scope = %d, want 0", len(store.locks))
	}

	unlock = store.lockScope(scope)
	store.commit(scope, map[string]any{"count": int64(1)}, nil, 10)
	unlock()
	if len(store.locks) != 1 {
		t.Fatalf("locks with committed state = %d, want 1", len(store.locks))
	}

	store.reset(scope)
	if len(store.locks) != 0 {
		t.Fatalf("locks after reset = %d, want 0", len(store.locks))
	}
}
