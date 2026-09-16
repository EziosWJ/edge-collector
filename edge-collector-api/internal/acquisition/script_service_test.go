package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
)

func TestScriptServiceLifecycleAndPublishedProjection(t *testing.T) {
	store := newScriptStoreFake()
	validator := ScriptValidatorFunc(func(context.Context, string) (ScriptValidationResult, error) {
		return ScriptValidationResult{Valid: true, Errors: []ScriptValidationError{}}, nil
	})
	service, err := NewService(store, validator)
	if err != nil {
		t.Fatal(err)
	}

	created, err := service.CreateScript(context.Background(), AuditMetadata{ActorID: 7}, ScriptInput{
		Name: "  ZNCK-I  ", Description: "  动态查询  ", DraftSource: "def after_poll(ctx):\n    pass\n",
	})
	if err != nil {
		t.Fatalf("CreateScript() error = %v", err)
	}
	if created.Name != "ZNCK-I" || created.Description != "动态查询" || created.PublishedVersion != nil || created.BoundDeviceCount != 0 {
		t.Fatalf("created projection = %+v", created)
	}

	validation, err := service.ValidateScript(context.Background(), created.ID)
	if err != nil || !validation.Valid || len(validation.Errors) != 0 {
		t.Fatalf("ValidateScript() = (%+v, %v)", validation, err)
	}

	first, err := service.PublishScript(context.Background(), AuditMetadata{ActorID: 7}, created.ID)
	if err != nil {
		t.Fatalf("PublishScript() error = %v", err)
	}
	if first.VersionNo != 1 || first.Source != created.DraftSource || first.PublishedBy != 7 {
		t.Fatalf("first version = %+v", first)
	}
	duplicate, err := service.PublishScript(context.Background(), AuditMetadata{ActorID: 7}, created.ID)
	if err != nil || duplicate.ID != first.ID {
		t.Fatalf("duplicate publish = (%+v, %v), want same version", duplicate, err)
	}

	updated, err := service.UpdateScript(context.Background(), AuditMetadata{ActorID: 8}, created.ID, ScriptInput{
		Name: "ZNCK-I", Description: "updated", DraftSource: "def after_poll(ctx):\n    print(\"next\")\n",
	})
	if err != nil {
		t.Fatalf("UpdateScript() error = %v", err)
	}
	if updated.PublishedVersionID == nil || *updated.PublishedVersionID != first.ID || updated.DraftMatchesPublished {
		t.Fatalf("draft update changed published projection = %+v", updated)
	}

	second, err := service.PublishScript(context.Background(), AuditMetadata{ActorID: 8}, created.ID)
	if err != nil || second.VersionNo != 2 {
		t.Fatalf("second publish = (%+v, %v)", second, err)
	}
	versions, err := service.ListScriptVersions(context.Background(), created.ID)
	if err != nil || len(versions) != 2 || versions[0].VersionNo != 2 {
		t.Fatalf("versions = (%+v, %v)", versions, err)
	}
	if err := service.RollbackScript(context.Background(), AuditMetadata{ActorID: 9}, created.ID, first.ID); err != nil {
		t.Fatalf("RollbackScript() error = %v", err)
	}
	current, err := service.FindScript(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.PublishedVersion == nil || current.PublishedVersion.ID != first.ID || current.DraftSource != updated.DraftSource {
		t.Fatalf("rollback projection = %+v", current)
	}

	if len(store.events) != 6 {
		t.Fatalf("audit events = %d, want create/publish/publish/update/publish/rollback", len(store.events))
	}
}

func TestScriptServiceRejectsInvalidPublishWithStructuredDiagnostics(t *testing.T) {
	store := newScriptStoreFake()
	service, err := NewService(store, ScriptValidatorFunc(func(context.Context, string) (ScriptValidationResult, error) {
		return ScriptValidationResult{
			Valid:  false,
			Errors: []ScriptValidationError{{Filename: "script.star", Line: 4, Column: 2, Message: "after_poll(ctx) 必须可调用"}},
		}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateScript(context.Background(), AuditMetadata{}, ScriptInput{Name: "invalid", DraftSource: "bad"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ValidateScript(context.Background(), created.ID)
	if err != nil || result.Valid || len(result.Errors) != 1 || result.Errors[0].Line != 4 {
		t.Fatalf("validation = (%+v, %v)", result, err)
	}
	_, err = service.PublishScript(context.Background(), AuditMetadata{}, created.ID)
	var validationErr *ScriptValidationFailedError
	if !errors.As(err, &validationErr) || validationErr.Result.Errors[0].Message != result.Errors[0].Message {
		t.Fatalf("publish error = %v, want structured validation failure", err)
	}
	if len(store.versions[created.ID]) != 0 {
		t.Fatal("invalid publish created a version")
	}
}

func TestScriptServiceBindsOnlyPublishedScriptsAndExposesEmptyRuntimeStates(t *testing.T) {
	store := newScriptStoreFake()
	store.device = &Device{ID: 42, Name: "设备", ChannelID: 3, UnitID: 1, DeviceType: DeviceTypeFeedProtector, Enabled: Enabled}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	unpublished, err := service.CreateScript(context.Background(), AuditMetadata{}, ScriptInput{Name: "draft", DraftSource: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.BindDeviceScript(context.Background(), AuditMetadata{}, 42, &unpublished.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bind unpublished error = %v, want %v", err, ErrInvalid)
	}

	service.SetScriptValidator(ScriptValidatorFunc(func(context.Context, string) (ScriptValidationResult, error) {
		return ScriptValidationResult{Valid: true}, nil
	}))
	if _, err := service.PublishScript(context.Background(), AuditMetadata{}, unpublished.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.BindDeviceScript(context.Background(), AuditMetadata{}, 42, &unpublished.ID); err != nil {
		t.Fatalf("bind published error = %v", err)
	}
	if store.bound[42] != unpublished.ID {
		t.Fatalf("bound script = %d, want %d", store.bound[42], unpublished.ID)
	}
	if err := service.BindDeviceScript(context.Background(), AuditMetadata{}, 42, nil); err != nil {
		t.Fatalf("unbind error = %v", err)
	}
	if _, ok := store.bound[42]; ok {
		t.Fatal("unbind left a binding")
	}

	states, err := service.ListScriptRuntimeStates(context.Background())
	if err != nil || states == nil || len(states) != 0 {
		t.Fatalf("empty runtime states = (%+v, %v)", states, err)
	}
	if _, err := service.FindScriptRuntimeState(context.Background(), 42); !errors.Is(err, ErrNotFound) {
		t.Fatalf("runtime detail error = %v, want %v", err, ErrNotFound)
	}
}

type scriptStoreFake struct {
	*transportStoreFake
	scripts       map[int64]Script
	versions      map[int64][]ScriptVersion
	bound         map[int64]int64
	events        []audit.Event
	nextScriptID  int64
	nextVersionID int64
}

func newScriptStoreFake() *scriptStoreFake {
	return &scriptStoreFake{
		transportStoreFake: &transportStoreFake{},
		scripts:            make(map[int64]Script),
		versions:           make(map[int64][]ScriptVersion),
		bound:              make(map[int64]int64),
		nextScriptID:       1,
		nextVersionID:      100,
	}
}

func (s *scriptStoreFake) PageScripts(_ context.Context, q ScriptQuery) (Page[Script], error) {
	records := make([]Script, 0, len(s.scripts))
	for _, value := range s.scripts {
		if value.Deleted == 0 && (q.Name == "" || strings.Contains(value.Name, q.Name)) {
			records = append(records, value)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return Page[Script]{Records: records, Total: int64(len(records)), Page: 1, PageSize: 20}, nil
}

func (s *scriptStoreFake) FindScript(_ context.Context, id int64) (*Script, error) {
	value, ok := s.scripts[id]
	if !ok || value.Deleted != 0 {
		return nil, ErrNotFound
	}
	return &value, nil
}

func (s *scriptStoreFake) ScriptNameExists(_ context.Context, name string, excludeID int64) (bool, error) {
	for id, value := range s.scripts {
		if id != excludeID && value.Deleted == 0 && value.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (s *scriptStoreFake) CountDevicesByScript(_ context.Context, id int64) (int64, error) {
	var count int64
	for _, scriptID := range s.bound {
		if scriptID == id {
			count++
		}
	}
	return count, nil
}

func (s *scriptStoreFake) CreateScript(_ context.Context, value Script, event audit.Event) (Script, error) {
	value.ID = s.nextScriptID
	s.nextScriptID++
	value.CreateTime, value.UpdateTime = time.Now().UTC(), time.Now().UTC()
	s.scripts[value.ID] = value
	event.ResourceID = value.ID
	s.events = append(s.events, event)
	return value, nil
}

func (s *scriptStoreFake) UpdateScript(_ context.Context, value Script, event audit.Event) (Script, error) {
	current, err := s.FindScript(context.Background(), value.ID)
	if err != nil {
		return Script{}, err
	}
	current.Name, current.Description, current.DraftSource = value.Name, value.Description, value.DraftSource
	current.UpdateTime = time.Now().UTC()
	s.scripts[value.ID] = *current
	event.ResourceID = value.ID
	s.events = append(s.events, event)
	return *current, nil
}

func (s *scriptStoreFake) DeleteScript(_ context.Context, id int64, event audit.Event) error {
	current, err := s.FindScript(context.Background(), id)
	if err != nil {
		return err
	}
	if count, _ := s.CountDevicesByScript(context.Background(), id); count > 0 {
		return ErrConflict
	}
	current.Deleted = 1
	s.scripts[id] = *current
	event.ResourceID = id
	s.events = append(s.events, event)
	return nil
}

func (s *scriptStoreFake) FindScriptVersion(_ context.Context, scriptID, versionID int64) (*ScriptVersion, error) {
	for _, value := range s.versions[scriptID] {
		if value.ID == versionID {
			copy := value
			return &copy, nil
		}
	}
	return nil, ErrNotFound
}

func (s *scriptStoreFake) ListScriptVersions(_ context.Context, scriptID int64) ([]ScriptVersion, error) {
	if _, err := s.FindScript(context.Background(), scriptID); err != nil {
		return nil, err
	}
	values := append([]ScriptVersion(nil), s.versions[scriptID]...)
	sort.Slice(values, func(i, j int) bool { return values[i].VersionNo > values[j].VersionNo })
	return values, nil
}

func (s *scriptStoreFake) PublishScript(_ context.Context, id int64, validate func(string) error, event audit.Event) (ScriptVersion, error) {
	current, err := s.FindScript(context.Background(), id)
	if err != nil {
		return ScriptVersion{}, err
	}
	if err := validate(current.DraftSource); err != nil {
		return ScriptVersion{}, err
	}
	checksum := sha256.Sum256([]byte(current.DraftSource))
	checksumText := hex.EncodeToString(checksum[:])
	if current.PublishedVersionID != nil {
		published, _ := s.FindScriptVersion(context.Background(), id, *current.PublishedVersionID)
		if published != nil && published.Checksum == checksumText {
			event.ResourceID = id
			s.events = append(s.events, event)
			return *published, nil
		}
	}
	version := ScriptVersion{ID: s.nextVersionID, ScriptID: id, VersionNo: len(s.versions[id]) + 1, Source: current.DraftSource, Checksum: checksumText, PublishedBy: event.Metadata.ActorID, PublishedAt: time.Now().UTC()}
	s.nextVersionID++
	s.versions[id] = append(s.versions[id], version)
	current.PublishedVersionID = &version.ID
	s.scripts[id] = *current
	event.ResourceID = id
	s.events = append(s.events, event)
	return version, nil
}

func (s *scriptStoreFake) RollbackScript(_ context.Context, id, versionID int64, event audit.Event) error {
	if _, err := s.FindScript(context.Background(), id); err != nil {
		return err
	}
	version, err := s.FindScriptVersion(context.Background(), id, versionID)
	if err != nil {
		return err
	}
	current, _ := s.FindScript(context.Background(), id)
	current.PublishedVersionID = &version.ID
	s.scripts[id] = *current
	event.ResourceID = id
	s.events = append(s.events, event)
	return nil
}

func (s *scriptStoreFake) BindDeviceScript(_ context.Context, deviceID int64, scriptID *int64, event audit.Event) error {
	if scriptID == nil {
		delete(s.bound, deviceID)
	} else {
		s.bound[deviceID] = *scriptID
	}
	event.ResourceID = deviceID
	s.events = append(s.events, event)
	return nil
}
