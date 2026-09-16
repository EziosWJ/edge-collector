package acquisition

import (
	"context"
	"errors"
	"strings"
	"time"

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
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		if err := ensureDeviceAddressAvailable(tx, value); err != nil {
			return err
		}
		if err := tx.Model(&Device{}).Where("id=? AND deleted=0", value.ID).Updates(map[string]any{
			"name": value.Name, "device_type": value.DeviceType, "channel_id": value.ChannelID,
			"unit_id": value.UnitID, "poll_interval_ms": value.PollIntervalMS,
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

func (r *Repository) DeleteDevice(ctx context.Context, id int64, event audit.Event) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Device{}).Where("id=? AND deleted=0", id).Update("deleted", 1).Error; err != nil {
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
