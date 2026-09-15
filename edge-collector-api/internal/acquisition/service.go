package acquisition

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
)

var (
	ErrNotFound          = errors.New("数据不存在")
	ErrInvalid           = errors.New("参数错误")
	ErrConflict          = errors.New("采集配置已存在")
	ErrChannelHasDevices = errors.New("通信通道已关联设备，禁止删除")
	ErrUnsupportedDevice = errors.New("不支持的设备类型")
)

type RuntimeRefresher func(context.Context) error

type Service struct {
	store     Store
	refresher RuntimeRefresher
}

func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("acquisition store is required")
	}
	return &Service{store: store}, nil
}

// SetRuntimeRefresher connects successful configuration writes to the running
// acquisition runtime. The database transaction remains the source of truth;
// a refresh failure is logged by the refresher and never changes the write
// result returned to the caller.
func (s *Service) SetRuntimeRefresher(refresher RuntimeRefresher) {
	s.refresher = refresher
}

func (s *Service) PageChannels(ctx context.Context, query ChannelQuery) (Page[Channel], error) {
	return s.store.PageChannels(ctx, query)
}

func (s *Service) FindChannel(ctx context.Context, id int64) (*Channel, error) {
	return s.store.FindChannel(ctx, id)
}

func (s *Service) CreateChannel(ctx context.Context, meta AuditMetadata, input ChannelInput) (Channel, error) {
	if err := validateChannel(input); err != nil {
		return Channel{}, err
	}
	value, err := s.store.CreateChannel(ctx, channelFrom(input, 0), auditEvent(meta, "acquisition.channel.create", "采集通信通道", 0))
	if err == nil {
		s.notifyRuntime(ctx)
	}
	return value, err
}

func (s *Service) UpdateChannel(ctx context.Context, meta AuditMetadata, id int64, input ChannelInput) (Channel, error) {
	if err := validateChannel(input); err != nil {
		return Channel{}, err
	}
	if _, err := s.store.FindChannel(ctx, id); err != nil {
		return Channel{}, err
	}
	value, err := s.store.UpdateChannel(ctx, channelFrom(input, id), auditEvent(meta, "acquisition.channel.update", "采集通信通道", id))
	if err == nil {
		s.notifyRuntime(ctx)
	}
	return value, err
}

func (s *Service) DeleteChannel(ctx context.Context, meta AuditMetadata, id int64) error {
	if _, err := s.store.FindChannel(ctx, id); err != nil {
		return err
	}
	count, err := s.store.CountDevicesByChannel(ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrChannelHasDevices
	}
	err = s.store.DeleteChannel(ctx, id, auditEvent(meta, "acquisition.channel.delete", "采集通信通道", id))
	if err == nil {
		s.notifyRuntime(ctx)
	}
	return err
}

func (s *Service) PageDevices(ctx context.Context, query DeviceQuery) (Page[Device], error) {
	return s.store.PageDevices(ctx, query)
}

func (s *Service) FindDevice(ctx context.Context, id int64) (*Device, error) {
	return s.store.FindDevice(ctx, id)
}

func (s *Service) CreateDevice(ctx context.Context, meta AuditMetadata, input DeviceInput) (Device, error) {
	if err := validateDevice(input); err != nil {
		return Device{}, err
	}
	if _, err := s.store.FindChannel(ctx, input.ChannelID); err != nil {
		return Device{}, err
	}
	return s.createDevice(ctx, meta, input, 0)
}

func (s *Service) UpdateDevice(ctx context.Context, meta AuditMetadata, id int64, input DeviceInput) (Device, error) {
	if err := validateDevice(input); err != nil {
		return Device{}, err
	}
	if _, err := s.store.FindDevice(ctx, id); err != nil {
		return Device{}, err
	}
	if _, err := s.store.FindChannel(ctx, input.ChannelID); err != nil {
		return Device{}, err
	}
	return s.createDevice(ctx, meta, input, id)
}

func (s *Service) createDevice(ctx context.Context, meta AuditMetadata, input DeviceInput, id int64) (Device, error) {
	exists, err := s.store.SlaveExists(ctx, input.ChannelID, input.SlaveID, id)
	if err != nil {
		return Device{}, err
	}
	if exists {
		return Device{}, ErrConflict
	}
	value := deviceFrom(input, id)
	if id == 0 {
		created, err := s.store.CreateDevice(ctx, value, auditEvent(meta, "acquisition.device.create", "采集设备", 0))
		if err == nil {
			s.notifyRuntime(ctx)
		}
		return created, err
	}
	updated, err := s.store.UpdateDevice(ctx, value, auditEvent(meta, "acquisition.device.update", "采集设备", id))
	if err == nil {
		s.notifyRuntime(ctx)
	}
	return updated, err
}

func (s *Service) DeleteDevice(ctx context.Context, meta AuditMetadata, id int64) error {
	if _, err := s.store.FindDevice(ctx, id); err != nil {
		return err
	}
	err := s.store.DeleteDevice(ctx, id, auditEvent(meta, "acquisition.device.delete", "采集设备", id))
	if err == nil {
		s.notifyRuntime(ctx)
	}
	return err
}

func (s *Service) EnabledConfiguration(ctx context.Context) ([]Channel, []Device, error) {
	return s.store.EnabledConfiguration(ctx)
}

type AuditEvent = audit.Event
type AuditMetadata = audit.Metadata

func validateChannel(input ChannelInput) error {
	input.Parity = strings.ToUpper(strings.TrimSpace(input.Parity))
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Port) == "" || input.BaudRate <= 0 || (input.DataBits != 7 && input.DataBits != 8) || (input.StopBits != 1 && input.StopBits != 2) || (input.Parity != "N" && input.Parity != "E" && input.Parity != "O") || input.TimeoutMS <= 0 || input.InterRequestDelayMS < 0 || input.InterRequestDelayMS > 60000 || (input.Enabled != Enabled && input.Enabled != Disabled) {
		return ErrInvalid
	}
	return nil
}

func validateDevice(input DeviceInput) error {
	if strings.TrimSpace(input.Name) == "" || input.DeviceType != DeviceTypeFeedProtector || input.ChannelID < 1 || input.SlaveID < 1 || input.SlaveID > 247 || input.PollIntervalMS <= 0 || input.FailureThreshold <= 0 || (input.Enabled != Enabled && input.Enabled != Disabled) {
		if input.DeviceType != DeviceTypeFeedProtector {
			return ErrUnsupportedDevice
		}
		return ErrInvalid
	}
	if err := validateRegisterBlocks(input.RegisterBlocks); err != nil {
		return err
	}
	return nil
}

func validateRegisterBlocks(blocks []RegisterBlockInput) error {
	names := make(map[string]struct{}, len(blocks))
	orders := make(map[int]struct{}, len(blocks))
	intervals := make(map[int][]registerInterval)
	allZeroOrder := len(blocks) > 1
	for _, block := range blocks {
		if block.SortOrder != 0 {
			allZeroOrder = false
			break
		}
	}
	for _, block := range blocks {
		name := strings.TrimSpace(block.Name)
		if name == "" || block.ID < 0 || block.SortOrder < 0 || (block.FunctionCode != FunctionCodeReadHoldingRegisters && block.FunctionCode != FunctionCodeReadInputRegisters) || block.StartAddress < 0 || block.StartAddress > 65535 || block.Quantity < 1 || block.Quantity > 125 || block.StartAddress+block.Quantity-1 > 65535 {
			return ErrInvalid
		}
		if _, exists := names[name]; exists {
			return ErrInvalid
		}
		names[name] = struct{}{}
		if _, exists := orders[block.SortOrder]; exists && !allZeroOrder {
			return ErrInvalid
		}
		orders[block.SortOrder] = struct{}{}
		intervals[block.FunctionCode] = append(intervals[block.FunctionCode], registerInterval{start: block.StartAddress, end: block.StartAddress + block.Quantity - 1})
	}
	for _, ranges := range intervals {
		for i := 0; i < len(ranges); i++ {
			for j := i + 1; j < len(ranges); j++ {
				if ranges[i].start <= ranges[j].end && ranges[j].start <= ranges[i].end {
					return ErrInvalid
				}
			}
		}
	}
	return nil
}

type registerInterval struct {
	start int
	end   int
}

func channelFrom(input ChannelInput, id int64) Channel {
	return Channel{ID: id, Name: strings.TrimSpace(input.Name), Port: strings.TrimSpace(input.Port), BaudRate: input.BaudRate, DataBits: input.DataBits, StopBits: input.StopBits, Parity: strings.ToUpper(strings.TrimSpace(input.Parity)), TimeoutMS: input.TimeoutMS, InterRequestDelayMS: input.InterRequestDelayMS, Enabled: input.Enabled}
}

func deviceFrom(input DeviceInput, id int64) Device {
	blocks := make([]RegisterBlock, len(input.RegisterBlocks))
	allZeroOrder := len(input.RegisterBlocks) > 1
	for _, block := range input.RegisterBlocks {
		if block.SortOrder != 0 {
			allZeroOrder = false
		}
	}
	for index, block := range input.RegisterBlocks {
		sortOrder := block.SortOrder
		if allZeroOrder {
			sortOrder = index
		}
		blocks[index] = RegisterBlock{
			ID:           block.ID,
			DeviceID:     id,
			Name:         strings.TrimSpace(block.Name),
			FunctionCode: block.FunctionCode,
			StartAddress: block.StartAddress,
			Quantity:     block.Quantity,
			SortOrder:    sortOrder,
		}
	}
	return Device{ID: id, Name: strings.TrimSpace(input.Name), DeviceType: input.DeviceType, ChannelID: input.ChannelID, SlaveID: input.SlaveID, PollIntervalMS: input.PollIntervalMS, FailureThreshold: input.FailureThreshold, Enabled: input.Enabled, RegisterBlocks: blocks}
}

func auditEvent(meta AuditMetadata, action, resource string, id int64) audit.Event {
	return audit.Event{Action: action, Resource: resource, ResourceID: id, Summary: action, Metadata: meta}
}

func (s *Service) notifyRuntime(ctx context.Context) {
	if s.refresher == nil {
		return
	}
	if err := s.refresher(ctx); err != nil {
		log.Printf("采集运行时刷新失败: %v", err)
	}
}
