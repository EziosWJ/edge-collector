//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
	platformdatabase "github.com/EziosWJ/edge-collector/edge-collector-api/internal/platform/database"
	"github.com/pressly/goose/v3"
)

func TestSQLiteAcquisitionScriptPersistence(t *testing.T) {
	db := openSQLiteDatabase(t, filepath.Join(t.TempDir(), "scripts.db"))
	defer db.Close()
	scriptPersistenceContract(t, db, "sqlite3", filepath.Join(projectRoot(t), "migrations", "sqlite", "schema"))
}

func TestPostgresAcquisitionScriptPersistence(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker daemon is required for PostgreSQL integration tests")
	}
	temporary := startPostgres(t)
	db := openTemporaryDatabase(t, temporary.dsn)
	defer db.Close()
	scriptPersistenceContract(t, db, "postgres", filepath.Join(projectRoot(t), "migrations", "schema"))
}

func scriptPersistenceContract(t *testing.T, db *platformdatabase.Database, dialect, dir string) {
	t.Helper()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(goose.SetDialect(dialect))
	goose.SetTableName("goose_schema_db_version")
	must(goose.UpToContext(ctx, db.SQL, dir, 11))
	// Existing configuration includes both soft-deleted data and dependent rows.
	must(db.GORM.Exec(`INSERT INTO acquisition_channel (id,name,protocol) VALUES (41,'legacy','MODBUS_TCP')`).Error)
	must(db.GORM.Exec(`INSERT INTO acquisition_device (id,name,device_type,channel_id,unit_id,deleted) VALUES (42,'legacy','FEED_PROTECTOR',41,7,0),(43,'deleted','FEED_PROTECTOR',41,8,1)`).Error)
	must(db.GORM.Exec(`INSERT INTO acquisition_network_device (device_id,host,port) VALUES (42,'localhost',502)`).Error)
	must(db.GORM.Exec(`INSERT INTO acquisition_register_block (device_id,name,function_code,start_address,quantity,sort_order) VALUES (42,'raw',3,8166,1,0)`).Error)
	must(goose.UpContext(ctx, db.SQL, dir))
	repo := acquisition.NewRepository(db.GORM)
	device, err := repo.FindDevice(ctx, 42)
	must(err)
	if device.ScriptID != nil || device.UnitID != 7 || device.NetworkEndpoint.Host != "localhost" || len(device.RegisterBlocks) != 1 {
		t.Fatalf("lost legacy configuration: %+v", device)
	}
	var deleted int
	must(db.GORM.Raw("SELECT deleted FROM acquisition_device WHERE id=43").Scan(&deleted).Error)
	if deleted != 1 {
		t.Fatal("lost soft-deleted device")
	}
	event := audit.Event{Action: "CREATE", Resource: "acquisition_script", Metadata: audit.Metadata{ActorID: 7}}
	validate := func(source string) error {
		if source == "bad" {
			return acquisition.ErrInvalid
		}
		return nil
	}
	script, err := repo.CreateScript(ctx, acquisition.Script{Name: "脚本", DraftSource: "源码\n"}, event)
	must(err)
	if script.PublishedVersionID != nil || script.CreateBy == nil || *script.CreateBy != 7 {
		t.Fatalf("bad draft: %+v", script)
	}
	if _, err := repo.CreateScript(ctx, acquisition.Script{Name: "无效 UTF-8", DraftSource: string([]byte{0xff})}, event); !errors.Is(err, acquisition.ErrInvalid) {
		t.Fatalf("invalid UTF-8 draft: %v", err)
	}
	if _, err := repo.CreateScript(ctx, acquisition.Script{Name: script.Name}, event); err == nil {
		t.Fatal("duplicate active name accepted")
	}
	device.ScriptID = &script.ID
	if _, err := repo.UpdateDevice(ctx, *device, event); !errors.Is(err, acquisition.ErrInvalid) {
		t.Fatalf("unpublished binding: %v", err)
	}
	v1, err := repo.PublishScript(ctx, script.ID, validate, event)
	must(err)
	if v1.VersionNo != 1 || v1.Source != script.DraftSource || v1.Checksum != fmt.Sprintf("%x", sha256.Sum256([]byte(script.DraftSource))) || v1.PublishedBy != 7 || time.Since(v1.PublishedAt) > time.Minute {
		t.Fatalf("bad version: %+v", v1)
	}
	same, err := repo.PublishScript(ctx, script.ID, validate, event)
	must(err)
	if same.ID != v1.ID {
		t.Fatal("duplicate publish created a version")
	}
	_, err = repo.UpdateDevice(ctx, *device, event)
	must(err)
	read, err := repo.FindDevice(ctx, device.ID)
	must(err)
	page, err := repo.PageDevices(ctx, acquisition.DeviceQuery{})
	must(err)
	_, enabled, err := repo.EnabledConfiguration(ctx)
	must(err)
	if read.ScriptID == nil || *read.ScriptID != script.ID || len(page.Records) != 1 || page.Records[0].ScriptID == nil || len(enabled) != 1 || enabled[0].ScriptID == nil {
		t.Fatal("binding not read consistently")
	}
	if err := repo.BindDeviceScript(ctx, device.ID, nil, event); err != nil {
		t.Fatalf("unbind through binding repository method: %v", err)
	}
	if err := repo.BindDeviceScript(ctx, device.ID, &script.ID, event); err != nil {
		t.Fatalf("rebind through binding repository method: %v", err)
	}
	// Competing publishes of the same draft must converge on one version.
	var group sync.WaitGroup
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			version, err := repo.PublishScript(ctx, script.ID, validate, event)
			if err == nil && version.ID != v1.ID {
				err = fmt.Errorf("concurrent publish returned version %d", version.ID)
			}
			results <- err
		}()
	}
	group.Wait()
	close(results)
	for err := range results {
		must(err)
	}
	if err := repo.DeleteScript(ctx, script.ID, event); !errors.Is(err, acquisition.ErrConflict) {
		t.Fatalf("bound delete: %v", err)
	}
	script.DraftSource = "next"
	updated, err := repo.UpdateScript(ctx, script, event)
	must(err)
	if updated.PublishedVersionID == nil || *updated.PublishedVersionID != v1.ID {
		t.Fatal("draft edit changed pointer")
	}
	// Failure after version insertion/pointer update must roll back both, including audit.
	must(db.GORM.Exec("ALTER TABLE sys_oper_log RENAME TO script_test_hidden_log").Error)
	if _, err := repo.PublishScript(ctx, script.ID, validate, event); err == nil {
		t.Fatal("publish ignored audit failure")
	}
	must(db.GORM.Exec("ALTER TABLE script_test_hidden_log RENAME TO sys_oper_log").Error)
	versions, err := repo.ListScriptVersions(ctx, script.ID)
	must(err)
	current, err := repo.FindScript(ctx, script.ID)
	must(err)
	if len(versions) != 1 || *current.PublishedVersionID != v1.ID {
		t.Fatal("failed publish leaked version/pointer")
	}
	versionResults := make(chan acquisition.ScriptVersion, 4)
	publishErrors := make(chan error, 4)
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			version, err := repo.PublishScript(ctx, script.ID, validate, event)
			versionResults <- version
			publishErrors <- err
		}()
	}
	group.Wait()
	close(versionResults)
	close(publishErrors)
	for err := range publishErrors {
		must(err)
	}
	var v2 acquisition.ScriptVersion
	for version := range versionResults {
		if v2.ID != 0 && v2.ID != version.ID {
			t.Fatal("concurrent publish duplicated version")
		}
		v2 = version
	}

	if v2.VersionNo != 2 {
		t.Fatal("failed publish consumed version number")
	}
	must(repo.RollbackScript(ctx, script.ID, v1.ID, event))
	current, err = repo.FindScript(ctx, script.ID)
	must(err)
	if *current.PublishedVersionID != v1.ID || current.DraftSource != "next" {
		t.Fatal("rollback changed draft or wrong pointer")
	}
	v3, err := repo.PublishScript(ctx, script.ID, validate, event)
	must(err)
	if v3.VersionNo != 3 {
		t.Fatal("version number reused after rollback")
	}
	other, err := repo.CreateScript(ctx, acquisition.Script{Name: "other", DraftSource: "other"}, event)
	must(err)
	foreign, err := repo.PublishScript(ctx, other.ID, validate, event)
	must(err)
	if err := repo.RollbackScript(ctx, script.ID, foreign.ID, event); !errors.Is(err, acquisition.ErrNotFound) {
		t.Fatalf("cross-script rollback: %v", err)
	}
	if _, err := repo.FindScriptVersion(ctx, other.ID, v1.ID); !errors.Is(err, acquisition.ErrNotFound) {
		t.Fatal("version lookup ignored ownership")
	}
	if err := db.GORM.Exec("UPDATE acquisition_script SET published_version_id=? WHERE id=?", foreign.ID, script.ID).Error; err == nil {
		t.Fatal("database accepted foreign pointer")
	}
	for _, statement := range []string{
		"UPDATE acquisition_script_version SET source='mutated' WHERE id=?",
		"UPDATE acquisition_script_version SET checksum='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' WHERE id=?",
		"UPDATE acquisition_script_version SET published_by=99 WHERE id=?",
		"UPDATE acquisition_script_version SET version_no=99 WHERE id=?",
		"DELETE FROM acquisition_script_version WHERE id=?",
	} {
		if err := db.GORM.Exec(statement, v1.ID).Error; err == nil {
			t.Fatalf("immutable version accepted %s", statement)
		}
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"UPDATE acquisition_script SET published_version_id=999999 WHERE id=?", []any{script.ID}},
		{"UPDATE acquisition_device SET script_id=999999 WHERE id=?", []any{device.ID}},
		{"DELETE FROM acquisition_script WHERE id=?", []any{script.ID}},
		{"INSERT INTO acquisition_script_version (script_id,version_no,source,checksum,published_by,published_at) VALUES (?,1,'duplicate',?,7,?)", []any{script.ID, v1.Checksum, v1.PublishedAt}},
	} {
		if err := db.GORM.Exec(statement.sql, statement.args...).Error; err == nil {
			t.Fatalf("constraint accepted %s", statement.sql)
		}
	}
	original, err := repo.FindScriptVersion(ctx, script.ID, v1.ID)
	must(err)
	if original.Source != v1.Source || original.Checksum != v1.Checksum || !original.PublishedAt.Truncate(time.Microsecond).Equal(v1.PublishedAt.Truncate(time.Microsecond)) {
		t.Fatal("history mutated")
	}
	script.DraftSource = "bad"
	_, err = repo.UpdateScript(ctx, script, event)
	must(err)
	if _, err := repo.PublishScript(ctx, script.ID, validate, event); !errors.Is(err, acquisition.ErrInvalid) {
		t.Fatalf("validation failed: %v", err)
	}
	versions, err = repo.ListScriptVersions(ctx, script.ID)
	must(err)
	if len(versions) != 3 || versions[0].VersionNo != 3 {
		t.Fatal("invalid publish or history order")
	}
	device.ScriptID = nil
	_, err = repo.UpdateDevice(ctx, *device, event)
	must(err)
	read, err = repo.FindDevice(ctx, device.ID)
	must(err)
	if read.ScriptID != nil {
		t.Fatal("unbind not persisted")
	}
	// Create also checks and persists bindings; device soft deletion clears them.
	created, err := repo.CreateDevice(ctx, acquisition.Device{Name: "bound", DeviceType: acquisition.DeviceTypeFeedProtector, ChannelID: 41, UnitID: 9, PollIntervalMS: 1000, FailureThreshold: 3, Enabled: 1, ScriptID: &script.ID}, event)
	must(err)
	must(repo.DeleteDevice(ctx, created.ID, event))
	must(repo.DeleteScript(ctx, script.ID, event))
	if _, err := repo.FindScript(ctx, script.ID); !errors.Is(err, acquisition.ErrNotFound) {
		t.Fatal("deleted script visible")
	}
	device.ScriptID = &script.ID
	if _, err := repo.UpdateDevice(ctx, *device, event); !errors.Is(err, acquisition.ErrInvalid) {
		t.Fatal("deleted script binding accepted")
	}
	_, err = repo.CreateScript(ctx, acquisition.Script{Name: script.Name}, event)
	must(err)
	scripts, err := repo.PageScripts(ctx, acquisition.ScriptQuery{PageSize: 1})
	must(err)
	if scripts.Total != 2 || len(scripts.Records) != 1 {
		t.Fatalf("bad script pagination: %+v", scripts)
	}
	var auditCount int64
	must(db.GORM.Table("sys_oper_log").Count(&auditCount).Error)
	if auditCount == 0 {
		t.Fatal("missing audit")
	}
	// Binding and soft deletion race through the same script identity lock.
	raceScript, err := repo.CreateScript(ctx, acquisition.Script{Name: "race", DraftSource: "race"}, event)
	must(err)
	_, err = repo.PublishScript(ctx, raceScript.ID, validate, event)
	must(err)
	device.ScriptID = &raceScript.ID
	start := make(chan struct{})
	raceResults := make(chan error, 2)
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		_, err := repo.UpdateDevice(ctx, *device, event)
		raceResults <- err
	}()
	go func() { defer group.Done(); <-start; raceResults <- repo.DeleteScript(ctx, raceScript.ID, event) }()
	close(start)
	group.Wait()
	close(raceResults)
	successes := 0
	for err := range raceResults {
		if err == nil {
			successes++
		} else if !errors.Is(err, acquisition.ErrConflict) && !errors.Is(err, acquisition.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("bind/delete race successes=%d", successes)
	}
	var dangling int64
	must(db.GORM.Raw("SELECT COUNT(*) FROM acquisition_device d JOIN acquisition_script s ON s.id=d.script_id WHERE s.deleted=1").Scan(&dangling).Error)
	if dangling != 0 {
		t.Fatal("binding/deletion race left dangling reference")
	}
	// Down/up must preserve pre-existing device configuration too.
	must(goose.DownToContext(ctx, db.SQL, dir, 11))
	must(goose.UpContext(ctx, db.SQL, dir))
	read, err = repo.FindDevice(ctx, 42)
	must(err)
	if read.ScriptID != nil || read.NetworkEndpoint.Host != "localhost" || len(read.RegisterBlocks) != 1 {
		t.Fatal("down/up lost configuration")
	}
	if dialect == "sqlite3" {
		var violations []struct{ Table string }
		must(db.GORM.Raw("PRAGMA foreign_key_check").Scan(&violations).Error)
		if len(violations) > 0 {
			t.Fatalf("foreign key violations: %+v", violations)
		}
	}
}
