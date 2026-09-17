package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Store interface {
	PageChannels(context.Context, ChannelQuery) (Page[Channel], error)
	FindChannel(context.Context, int64) (*Channel, error)
	CreateChannel(context.Context, Channel, audit.Event) (Channel, error)
	UpdateChannel(context.Context, Channel, audit.Event) (Channel, error)
	DeleteChannel(context.Context, int64, audit.Event) error
	CountDevicesByChannel(context.Context, int64) (int64, error)

	PageDevices(context.Context, DeviceQuery) (Page[Device], error)
	FindDevice(context.Context, int64) (*Device, error)
	UnitIDExists(context.Context, int64, uint8, int64) (bool, error)
	NetworkEndpointExists(context.Context, int64, string, int, uint8, int64) (bool, error)
	CreateDevice(context.Context, Device, audit.Event) (Device, error)
	UpdateDevice(context.Context, Device, audit.Event) (Device, error)
	DeleteDevice(context.Context, int64, audit.Event) error
	BindDeviceScript(context.Context, int64, *int64, audit.Event) error

	PageScripts(context.Context, ScriptQuery) (Page[Script], error)
	FindScript(context.Context, int64) (*Script, error)
	ScriptNameExists(context.Context, string, int64) (bool, error)
	CountDevicesByScript(context.Context, int64) (int64, error)
	CreateScript(context.Context, Script, audit.Event) (Script, error)
	UpdateScript(context.Context, Script, audit.Event) (Script, error)
	DeleteScript(context.Context, int64, audit.Event) error
	FindScriptVersion(context.Context, int64, int64) (*ScriptVersion, error)
	ListScriptVersions(context.Context, int64) ([]ScriptVersion, error)
	PublishScript(context.Context, int64, func(string) error, audit.Event) (ScriptVersion, error)
	RollbackScript(context.Context, int64, int64, audit.Event) error

	EnabledConfiguration(context.Context) ([]Channel, []Device, error)
}

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

func (r *Repository) PageChannels(ctx context.Context, q ChannelQuery) (Page[Channel], error) {
	q = normalizeChannelQuery(q)
	db := r.db.WithContext(ctx).Model(&Channel{}).Where("deleted=0")
	var page Page[Channel]
	if err := db.Count(&page.Total).Error; err != nil {
		return page, err
	}
	page.Page, page.PageSize = q.Page, q.PageSize
	err := db.Order("id").Offset((q.Page - 1) * q.PageSize).Limit(q.PageSize).Find(&page.Records).Error
	if err != nil {
		return page, err
	}
	return page, r.loadSerialConfigs(ctx, page.Records)
}

func (r *Repository) FindChannel(ctx context.Context, id int64) (*Channel, error) {
	var value Channel
	err := r.db.WithContext(ctx).Where("id=? AND deleted=0", id).Take(&value).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return &value, err
	}
	channels := []Channel{value}
	if err := r.loadSerialConfigs(ctx, channels); err != nil {
		return nil, err
	}
	value = channels[0]
	return &value, nil
}

func (r *Repository) CreateChannel(ctx context.Context, value Channel, event audit.Event) (Channel, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&value).Error; err != nil {
			return err
		}
		if err := saveSerialConfig(tx, value.ID, value.SerialConfig); err != nil {
			return err
		}
		event.ResourceID = value.ID
		return audit.RecordOn(ctx, tx, event)
	})
	return value, err
}

func (r *Repository) UpdateChannel(ctx context.Context, value Channel, event audit.Event) (Channel, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Channel{}).Where("id=? AND deleted=0", value.ID).Updates(map[string]any{
			"name":       value.Name,
			"timeout_ms": value.TimeoutMS, "inter_request_delay_ms": value.InterRequestDelayMS,
			"enabled": value.Enabled, "update_time": time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
		if err := saveSerialConfig(tx, value.ID, value.SerialConfig); err != nil {
			return err
		}
		return audit.RecordOn(ctx, tx, event)
	})
	return value, err
}

func (r *Repository) DeleteChannel(ctx context.Context, id int64, event audit.Event) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Channel{}).Where("id=? AND deleted=0", id).Update("deleted", 1).Error; err != nil {
			return err
		}
		return audit.RecordOn(ctx, tx, event)
	})
}

func (r *Repository) CountDevicesByChannel(ctx context.Context, id int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Device{}).Where("channel_id=? AND deleted=0", id).Count(&count).Error
	return count, err
}

func (r *Repository) CountDevicesByScript(ctx context.Context, id int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Device{}).Where("script_id=? AND deleted=0", id).Count(&count).Error
	return count, err
}

func (r *Repository) ScriptNameExists(ctx context.Context, name string, excludeID int64) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Script{}).Where("name=? AND deleted=0 AND id<>?", strings.TrimSpace(name), excludeID).Count(&count).Error
	return count > 0, err
}

func (r *Repository) PageDevices(ctx context.Context, q DeviceQuery) (Page[Device], error) {
	q = normalizeDeviceQuery(q)
	db := r.db.WithContext(ctx).Model(&Device{}).Where("deleted=0")
	if q.ChannelID != nil {
		db = db.Where("channel_id=?", *q.ChannelID)
	}
	if q.Enabled != nil {
		db = db.Where("enabled=?", *q.Enabled)
	}
	var page Page[Device]
	if err := db.Count(&page.Total).Error; err != nil {
		return page, err
	}
	page.Page, page.PageSize = q.Page, q.PageSize
	err := db.Order("id").Offset((q.Page - 1) * q.PageSize).Limit(q.PageSize).Find(&page.Records).Error
	if err != nil {
		return page, err
	}
	err = r.loadRegisterBlocks(ctx, page.Records)
	if err != nil {
		return page, err
	}
	return page, r.loadNetworkEndpoints(ctx, page.Records)
}

func (r *Repository) FindDevice(ctx context.Context, id int64) (*Device, error) {
	var value Device
	err := r.db.WithContext(ctx).Where("id=? AND deleted=0", id).Take(&value).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	devices := []Device{value}
	if err := r.loadRegisterBlocks(ctx, devices); err != nil {
		return nil, err
	}
	value = devices[0]
	if err := r.loadNetworkEndpoints(ctx, devices); err != nil {
		return nil, err
	}
	value = devices[0]
	return &value, nil
}

func (r *Repository) FindDeviceByExternalID(ctx context.Context, externalID string) (*Device, error) {
	var value Device
	err := r.db.WithContext(ctx).Where("external_id=? AND deleted=0", strings.TrimSpace(externalID)).Take(&value).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	devices := []Device{value}
	if err := r.loadRegisterBlocks(ctx, devices); err != nil {
		return nil, err
	}
	if err := r.loadNetworkEndpoints(ctx, devices); err != nil {
		return nil, err
	}
	value = devices[0]
	return &value, nil
}

func (r *Repository) UnitIDExists(ctx context.Context, channelID int64, unitID uint8, excludeID int64) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Device{}).Where(
		"channel_id=? AND unit_id=? AND deleted=0 AND id<>?", channelID, unitID, excludeID,
	).Count(&count).Error
	return count > 0, err
}

func (r *Repository) NetworkEndpointExists(ctx context.Context, channelID int64, host string, port int, unitID uint8, excludeID int64) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Device{}).
		Joins("JOIN acquisition_network_device ON acquisition_network_device.device_id = acquisition_device.id").
		Where("acquisition_device.channel_id=? AND acquisition_device.unit_id=? AND acquisition_device.deleted=0 AND acquisition_device.id<>? AND acquisition_network_device.host=? AND acquisition_network_device.port=?", channelID, unitID, excludeID, strings.TrimSpace(host), port).
		Count(&count).Error
	return count > 0, err
}

func (r *Repository) CreateDevice(ctx context.Context, value Device, event audit.Event) (Device, error) {
	if strings.TrimSpace(value.ExternalID) == "" {
		value.ExternalID = newExternalID()
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureDeviceScriptAvailable(tx, value.ScriptID); err != nil {
			return err
		}
		if err := ensureDeviceAddressAvailable(tx, value); err != nil {
			return err
		}
		if err := tx.Create(&value).Error; err != nil {
			return err
		}
		if err := saveRegisterBlocks(tx, value.ID, value.RegisterBlocks); err != nil {
			return err
		}
		if err := saveNetworkEndpoint(tx, value.ID, value.NetworkEndpoint); err != nil {
			return err
		}
		event.ResourceID = value.ID
		return audit.RecordOn(ctx, tx, event)
	})
	return value, err
}

func (r *Repository) UpdateDevice(ctx context.Context, value Device, event audit.Event) (Device, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureDeviceScriptAvailable(tx, value.ScriptID); err != nil {
			return err
		}
		if err := ensureDeviceAddressAvailable(tx, value); err != nil {
			return err
		}
		if err := tx.Model(&Device{}).Where("id=? AND deleted=0", value.ID).Updates(map[string]any{
			"external_id": value.ExternalID, "name": value.Name, "device_type": value.DeviceType, "channel_id": value.ChannelID,
			"unit_id": value.UnitID, "poll_interval_ms": value.PollIntervalMS, "script_id": value.ScriptID,
			"failure_threshold": value.FailureThreshold, "enabled": value.Enabled, "update_time": time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
		if err := saveRegisterBlocks(tx, value.ID, value.RegisterBlocks); err != nil {
			return err
		}
		if err := saveNetworkEndpoint(tx, value.ID, value.NetworkEndpoint); err != nil {
			return err
		}
		return audit.RecordOn(ctx, tx, event)
	})
	return value, err
}

// BindDeviceScript changes only the nullable script binding. It keeps the
// operation atomic with its audit record and permits an explicit nil to
// unbind a device without involving the rest of its acquisition settings.
func (r *Repository) BindDeviceScript(ctx context.Context, deviceID int64, scriptID *int64, event audit.Event) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureDeviceScriptAvailable(tx, scriptID); err != nil {
			return err
		}
		query := tx.Model(&Device{}).Where("id=? AND deleted=0", deviceID)
		if tx.Dialector.Name() == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var device Device
		if err := query.Take(&device).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if err := tx.Model(&Device{}).Where("id=? AND deleted=0", deviceID).Updates(map[string]any{
			"script_id": scriptID, "update_time": time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
		event.ResourceID = deviceID
		return audit.RecordOn(ctx, tx, event)
	})
}

func (r *Repository) DeleteDevice(ctx context.Context, id int64, event audit.Event) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Device{}).Where("id=? AND deleted=0", id).Updates(map[string]any{"deleted": 1, "script_id": nil}).Error; err != nil {
			return err
		}
		return audit.RecordOn(ctx, tx, event)
	})
}

func (r *Repository) EnabledConfiguration(ctx context.Context) ([]Channel, []Device, error) {
	var channels []Channel
	if err := r.db.WithContext(ctx).Where("enabled=1 AND deleted=0").Order("id").Find(&channels).Error; err != nil {
		return nil, nil, err
	}
	var devices []Device
	if err := r.db.WithContext(ctx).Where("enabled=1 AND deleted=0").Order("channel_id,id").Find(&devices).Error; err != nil {
		return nil, nil, err
	}
	if err := r.loadSerialConfigs(ctx, channels); err != nil {
		return nil, nil, err
	}
	if err := r.loadRegisterBlocks(ctx, devices); err != nil {
		return nil, nil, err
	}
	if err := r.loadNetworkEndpoints(ctx, devices); err != nil {
		return nil, nil, err
	}
	return channels, devices, nil
}

func (r *Repository) loadSerialConfigs(ctx context.Context, channels []Channel) error {
	if len(channels) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(channels))
	byID := make(map[int64]int)
	for index := range channels {
		ids = append(ids, channels[index].ID)
		byID[channels[index].ID] = index
		channels[index].SerialConfig = nil
	}
	var values []SerialChannel
	if err := r.db.WithContext(ctx).Where("channel_id IN ?", ids).Find(&values).Error; err != nil {
		return err
	}
	for _, value := range values {
		if index, ok := byID[value.ChannelID]; ok {
			channels[index].SerialConfig = &SerialConfig{Port: value.Port, BaudRate: value.BaudRate, DataBits: value.DataBits, StopBits: value.StopBits, Parity: value.Parity}
		}
	}
	return nil
}

func (r *Repository) loadNetworkEndpoints(ctx context.Context, devices []Device) error {
	if len(devices) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(devices))
	byID := make(map[int64]int)
	for index := range devices {
		ids = append(ids, devices[index].ID)
		byID[devices[index].ID] = index
		devices[index].NetworkEndpoint = nil
	}
	var values []NetworkDevice
	if err := r.db.WithContext(ctx).Where("device_id IN ?", ids).Find(&values).Error; err != nil {
		return err
	}
	for _, value := range values {
		if index, ok := byID[value.DeviceID]; ok {
			devices[index].NetworkEndpoint = &NetworkEndpoint{Host: value.Host, Port: value.Port}
		}
	}
	return nil
}

func saveSerialConfig(tx *gorm.DB, channelID int64, config *SerialConfig) error {
	if err := tx.Where("channel_id=?", channelID).Delete(&SerialChannel{}).Error; err != nil {
		return err
	}
	if config == nil {
		return nil
	}
	return tx.Create(&SerialChannel{ChannelID: channelID, Port: config.Port, BaudRate: config.BaudRate, DataBits: config.DataBits, StopBits: config.StopBits, Parity: config.Parity}).Error
}

func saveNetworkEndpoint(tx *gorm.DB, deviceID int64, endpoint *NetworkEndpoint) error {
	if err := tx.Where("device_id=?", deviceID).Delete(&NetworkDevice{}).Error; err != nil {
		return err
	}
	if endpoint == nil {
		return nil
	}
	return tx.Create(&NetworkDevice{DeviceID: deviceID, Host: strings.TrimSpace(endpoint.Host), Port: endpoint.Port}).Error
}

func ensureDeviceAddressAvailable(tx *gorm.DB, value Device) error {
	channelQuery := tx.Where("id=? AND deleted=0", value.ChannelID)
	if tx.Dialector.Name() == "postgres" {
		channelQuery = channelQuery.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var channel Channel
	if err := channelQuery.Take(&channel).Error; err != nil {
		return err
	}

	query := tx.Model(&Device{}).Where(
		"acquisition_device.channel_id=? AND acquisition_device.unit_id=? AND acquisition_device.deleted=0 AND acquisition_device.id<>?",
		value.ChannelID, value.UnitID, value.ID,
	)
	if value.NetworkEndpoint != nil {
		query = query.Joins("JOIN acquisition_network_device ON acquisition_network_device.device_id = acquisition_device.id").Where(
			"acquisition_network_device.host=? AND acquisition_network_device.port=?",
			strings.TrimSpace(value.NetworkEndpoint.Host), value.NetworkEndpoint.Port,
		)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) loadRegisterBlocks(ctx context.Context, devices []Device) error {
	if len(devices) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(devices))
	byID := make(map[int64]int, len(devices))
	for index := range devices {
		ids = append(ids, devices[index].ID)
		byID[devices[index].ID] = index
		devices[index].RegisterBlocks = []RegisterBlock{}
	}
	var blocks []RegisterBlock
	if err := r.db.WithContext(ctx).Where("device_id IN ?", ids).Order("device_id, sort_order, id").Find(&blocks).Error; err != nil {
		return err
	}
	for _, block := range blocks {
		if index, ok := byID[block.DeviceID]; ok {
			devices[index].RegisterBlocks = append(devices[index].RegisterBlocks, block)
		}
	}
	return nil
}

func saveRegisterBlocks(tx *gorm.DB, deviceID int64, blocks []RegisterBlock) error {
	var existing []RegisterBlock
	if err := tx.Where("device_id=?", deviceID).Find(&existing).Error; err != nil {
		return err
	}
	existingByID := make(map[int64]RegisterBlock, len(existing))
	for _, block := range existing {
		existingByID[block.ID] = block
	}
	incomingIDs := make(map[int64]struct{}, len(blocks))
	for _, block := range blocks {
		if block.ID > 0 {
			if _, ok := existingByID[block.ID]; !ok {
				return ErrInvalid
			}
			incomingIDs[block.ID] = struct{}{}
		}
	}
	for _, block := range existing {
		if _, keep := incomingIDs[block.ID]; keep {
			continue
		}
		if err := tx.Where("id=? AND device_id=?", block.ID, deviceID).Delete(&RegisterBlock{}).Error; err != nil {
			return err
		}
	}
	for index := range blocks {
		block := blocks[index]
		block.DeviceID = deviceID
		block.UpdateTime = time.Now().UTC()
		if block.ID == 0 {
			if err := tx.Create(&block).Error; err != nil {
				return err
			}
			blocks[index] = block
			continue
		}
		if err := tx.Model(&RegisterBlock{}).Where("id=? AND device_id=?", block.ID, deviceID).Updates(map[string]any{
			"name": block.Name, "function_code": block.FunctionCode, "start_address": block.StartAddress,
			"quantity": block.Quantity, "sort_order": block.SortOrder, "update_time": block.UpdateTime,
		}).Error; err != nil {
			return err
		}
		blocks[index] = block
	}
	return nil
}

func normalizeChannelQuery(q ChannelQuery) ChannelQuery {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = 20
	}
	if q.PageSize > 500 {
		q.PageSize = 500
	}
	return q
}

func normalizeDeviceQuery(q DeviceQuery) DeviceQuery {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = 20
	}
	if q.PageSize > 500 {
		q.PageSize = 500
	}
	return q
}

func normalizeScriptQuery(q ScriptQuery) ScriptQuery {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = 20
	}
	if q.PageSize > 500 {
		q.PageSize = 500
	}
	return q
}

func (r *Repository) PageScripts(ctx context.Context, q ScriptQuery) (Page[Script], error) {
	paging := normalizeScriptQuery(q)
	page := Page[Script]{Page: paging.Page, PageSize: paging.PageSize}
	db := r.db.WithContext(ctx).Model(&Script{}).Where("deleted=0")
	if name := strings.TrimSpace(paging.Name); name != "" {
		db = db.Where("name LIKE ?", "%"+name+"%")
	}
	if err := db.Count(&page.Total).Error; err != nil {
		return page, err
	}
	err := db.Order("id").Offset((page.Page - 1) * page.PageSize).Limit(page.PageSize).Find(&page.Records).Error
	return page, err
}

func (r *Repository) FindScript(ctx context.Context, id int64) (*Script, error) {
	return findScript(r.db.WithContext(ctx), id, false)
}

// Lock the identity before version allocation, binding, or deletion. PostgreSQL
// row locks and SQLite write transactions serialize competing configuration writes.
func findScript(tx *gorm.DB, id int64, lock bool) (*Script, error) {
	query := tx.Where("id=? AND deleted=0", id)
	if lock && tx.Dialector.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var value Script
	err := query.Take(&value).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &value, err
}

func (r *Repository) CreateScript(ctx context.Context, value Script, event audit.Event) (Script, error) {
	if !utf8.ValidString(value.DraftSource) {
		return Script{}, ErrInvalid
	}
	// Callers cannot create a published identity or supply persistence metadata.
	value.ID, value.Deleted, value.PublishedVersionID = 0, 0, nil
	actorID := event.Metadata.ActorID
	value.CreateBy, value.UpdateBy = &actorID, &actorID
	now := time.Now().UTC()
	value.CreateTime, value.UpdateTime = now, now
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&value).Error; err != nil {
			return err
		}
		event.ResourceID = value.ID
		return audit.RecordOn(ctx, tx, event)
	})
	return value, err
}

func (r *Repository) UpdateScript(ctx context.Context, value Script, event audit.Event) (Script, error) {
	if !utf8.ValidString(value.DraftSource) {
		return Script{}, ErrInvalid
	}
	var result Script
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := findScript(tx, value.ID, true)
		if err != nil {
			return err
		}
		current.Name, current.Description, current.DraftSource = value.Name, value.Description, value.DraftSource
		actorID := event.Metadata.ActorID
		current.UpdateBy, current.UpdateTime = &actorID, time.Now().UTC()
		if err := tx.Model(current).Updates(map[string]any{
			"name": current.Name, "description": current.Description, "draft_source": current.DraftSource,
			"update_by": current.UpdateBy, "update_time": current.UpdateTime,
		}).Error; err != nil {
			return err
		}
		result = *current
		event.ResourceID = value.ID
		return audit.RecordOn(ctx, tx, event)
	})
	return result, err
}

func (r *Repository) DeleteScript(ctx context.Context, id int64, event audit.Event) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := findScript(tx, id, true); err != nil {
			return err
		}
		var count int64
		// Include any retained binding, even on a legacy soft-deleted device.
		if err := tx.Model(&Device{}).Where("script_id=?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrConflict
		}
		if err := tx.Model(&Script{}).Where("id=?", id).Updates(map[string]any{
			"deleted": 1, "update_by": event.Metadata.ActorID, "update_time": time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
		event.ResourceID = id
		return audit.RecordOn(ctx, tx, event)
	})
}

func (r *Repository) FindScriptVersion(ctx context.Context, scriptID, versionID int64) (*ScriptVersion, error) {
	tx := r.db.WithContext(ctx)
	if _, err := findScript(tx, scriptID, false); err != nil {
		return nil, err
	}
	return findScriptVersion(tx, scriptID, versionID)
}

func findScriptVersion(tx *gorm.DB, scriptID, versionID int64) (*ScriptVersion, error) {
	var value ScriptVersion
	err := tx.Where("script_id=? AND id=?", scriptID, versionID).Take(&value).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &value, err
}

func (r *Repository) ListScriptVersions(ctx context.Context, scriptID int64) ([]ScriptVersion, error) {
	tx := r.db.WithContext(ctx)
	if _, err := findScript(tx, scriptID, false); err != nil {
		return nil, err
	}
	values := []ScriptVersion{}
	err := tx.Where("script_id=?", scriptID).Order("version_no DESC").Find(&values).Error
	return values, err
}

// PublishScript validates and snapshots the current draft in one transaction.
// The optional validator lets the future service perform compilation while the
// repository still re-reads the exact draft protected by the transaction. A
// nil validator is useful to persistence-only callers and does not add a
// dependency on the Starlark runtime.
func (r *Repository) PublishScript(ctx context.Context, id int64, validate func(string) error, event audit.Event) (ScriptVersion, error) {
	var result ScriptVersion
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		script, err := findScript(tx, id, true)
		if err != nil {
			return err
		}
		if validate != nil {
			if err := validate(script.DraftSource); err != nil {
				return err
			}
		}
		checksum, err := sourceChecksum(script.DraftSource)
		if err != nil {
			return err
		}
		if script.PublishedVersionID != nil {
			current, err := findScriptVersion(tx, id, *script.PublishedVersionID)
			if err != nil {
				return err
			}
			if current.Checksum == checksum {
				result = *current
				event.ResourceID = id
				return audit.RecordOn(ctx, tx, event)
			}
		}
		var last int
		if err := tx.Model(&ScriptVersion{}).Where("script_id=?", id).Select("COALESCE(MAX(version_no), 0)").Scan(&last).Error; err != nil {
			return err
		}
		result = ScriptVersion{ScriptID: id, VersionNo: last + 1, Source: script.DraftSource,
			Checksum: checksum, PublishedBy: event.Metadata.ActorID, PublishedAt: time.Now().UTC()}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if err := updatePublishedPointer(tx, id, result.ID, event.Metadata.ActorID); err != nil {
			return err
		}
		event.ResourceID = id
		return audit.RecordOn(ctx, tx, event)
	})
	if err != nil {
		return ScriptVersion{}, err
	}
	return result, nil
}

func (r *Repository) RollbackScript(ctx context.Context, id, versionID int64, event audit.Event) error {
	return r.SetPublishedVersion(ctx, id, versionID, event)
}

// SetPublishedVersion switches the published pointer to a version owned by
// the same active script. The composite foreign key is the final database
// guard; the ownership check also gives callers a stable ErrNotFound.
func (r *Repository) SetPublishedVersion(ctx context.Context, id, versionID int64, event audit.Event) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := findScript(tx, id, true); err != nil {
			return err
		}
		if _, err := findScriptVersion(tx, id, versionID); err != nil {
			return err
		}
		if err := updatePublishedPointer(tx, id, versionID, event.Metadata.ActorID); err != nil {
			return err
		}
		event.ResourceID = id
		return audit.RecordOn(ctx, tx, event)
	})
}

func updatePublishedPointer(tx *gorm.DB, id, versionID, actorID int64) error {
	return tx.Model(&Script{}).Where("id=? AND deleted=0", id).Updates(map[string]any{
		"published_version_id": versionID, "update_by": actorID, "update_time": time.Now().UTC(),
	}).Error
}

func ensureDeviceScriptAvailable(tx *gorm.DB, id *int64) error {
	if id == nil {
		return nil
	}
	script, err := findScript(tx, *id, true)
	if errors.Is(err, ErrNotFound) {
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	if script.PublishedVersionID == nil {
		return ErrInvalid
	}
	return nil
}

func sourceChecksum(source string) (string, error) {
	if !utf8.ValidString(source) {
		return "", ErrInvalid
	}
	digest := sha256.Sum256([]byte(source))
	return hex.EncodeToString(digest[:]), nil
}
