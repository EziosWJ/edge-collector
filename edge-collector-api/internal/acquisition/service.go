package acquisition

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
)

var (
	ErrNotFound                   = errors.New("数据不存在")
	ErrInvalid                    = errors.New("参数错误")
	ErrConflict                   = errors.New("采集配置已存在")
	ErrChannelHasDevices          = errors.New("通信通道已关联设备，禁止删除")
	ErrUnsupportedDevice          = errors.New("不支持的设备类型")
	ErrProtocolImmutable          = errors.New("通信通道协议不可修改")
	ErrCrossProtocolMove          = errors.New("设备不能跨协议移动")
	ErrScriptValidatorUnavailable = errors.New("脚本校验器未配置")
)

type RuntimeRefresher func(context.Context) error

// ScriptValidator is the boundary to the controlled Starlark compiler owned
// by the script subsystem. Keeping the boundary here avoids coupling the
// management API to a concrete interpreter implementation.
type ScriptValidator interface {
	Validate(context.Context, string) (ScriptValidationResult, error)
}

type ScriptValidatorFunc func(context.Context, string) (ScriptValidationResult, error)

func (f ScriptValidatorFunc) Validate(ctx context.Context, source string) (ScriptValidationResult, error) {
	return f(ctx, source)
}

// ScriptRuntimeStateReader is an optional observation seam. Until the channel
// runtime publishes script execution state, the service returns an explicit
// empty result instead of manufacturing execution records.
type ScriptRuntimeStateReader interface {
	List(context.Context) ([]ScriptRuntimeState, error)
	Get(context.Context, int64) (*ScriptRuntimeState, error)
}

// ScriptValidationFailedError preserves structured validator diagnostics for
// the publish endpoint, which must reject an invalid draft with HTTP 400.
type ScriptValidationFailedError struct {
	Result ScriptValidationResult
}

func (e *ScriptValidationFailedError) Error() string { return "脚本校验未通过" }

type Service struct {
	store         Store
	refresher     RuntimeRefresher
	validator     ScriptValidator
	runtimeStates ScriptRuntimeStateReader
}

func NewService(store Store, validators ...ScriptValidator) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("acquisition store is required")
	}
	service := &Service{store: store}
	if len(validators) > 0 {
		service.validator = validators[0]
	}
	return service, nil
}

// SetRuntimeRefresher connects successful configuration writes to the running
// acquisition runtime. The database transaction remains the source of truth;
// a refresh failure is logged by the refresher and never changes the write
// result returned to the caller.
func (s *Service) SetRuntimeRefresher(refresher RuntimeRefresher) {
	s.refresher = refresher
}

func (s *Service) SetScriptValidator(validator ScriptValidator) {
	s.validator = validator
}

func (s *Service) SetScriptRuntimeStateReader(reader ScriptRuntimeStateReader) {
	s.runtimeStates = reader
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
	existing, err := s.store.FindChannel(ctx, id)
	if err != nil {
		return Channel{}, err
	}
	if existing.Protocol != input.Protocol {
		return Channel{}, fmt.Errorf("%w: %s -> %s", ErrProtocolImmutable, existing.Protocol, input.Protocol)
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

// FindDeviceByExternalID resolves the stable MQTT identity without exposing
// the database primary key to the transport layer. The concrete repository
// implements this lookup; test stores that predate MQTT can return the same
// domain not-found error through the optional seam.
func (s *Service) FindDeviceByExternalID(ctx context.Context, externalID string) (*Device, error) {
	resolver, ok := s.store.(interface {
		FindDeviceByExternalID(context.Context, string) (*Device, error)
	})
	if !ok {
		return nil, ErrNotFound
	}
	return resolver.FindDeviceByExternalID(ctx, externalID)
}

// PublishedScriptVersion resolves only the immutable version currently
// selected by a device's active script identity. Draft source is deliberately
// never returned to the acquisition runtime.
func (s *Service) PublishedScriptVersion(ctx context.Context, device Device) (*ScriptVersion, error) {
	if device.ScriptID == nil {
		return nil, nil
	}
	if *device.ScriptID < 1 {
		return nil, ErrInvalid
	}
	script, err := s.store.FindScript(ctx, *device.ScriptID)
	if err != nil {
		return nil, err
	}
	if script.PublishedVersionID == nil {
		return nil, nil
	}
	return s.store.FindScriptVersion(ctx, script.ID, *script.PublishedVersionID)
}

func (s *Service) CreateDevice(ctx context.Context, meta AuditMetadata, input DeviceInput) (Device, error) {
	if err := validateDevice(input); err != nil {
		return Device{}, err
	}
	channel, err := s.store.FindChannel(ctx, input.ChannelID)
	if err != nil {
		return Device{}, err
	}
	if err := validateDeviceForProtocol(input, channel.Protocol); err != nil {
		return Device{}, err
	}
	if err := s.validateDeviceScript(ctx, input.ScriptID); err != nil {
		return Device{}, err
	}
	return s.createDevice(ctx, meta, input, channel.Protocol, 0)
}

func (s *Service) UpdateDevice(ctx context.Context, meta AuditMetadata, id int64, input DeviceInput) (Device, error) {
	if err := validateDevice(input); err != nil {
		return Device{}, err
	}
	if _, err := s.store.FindDevice(ctx, id); err != nil {
		return Device{}, err
	}
	channel, err := s.store.FindChannel(ctx, input.ChannelID)
	if err != nil {
		return Device{}, err
	}
	current, err := s.store.FindDevice(ctx, id)
	if err != nil {
		return Device{}, err
	}
	currentChannel, err := s.store.FindChannel(ctx, current.ChannelID)
	if err != nil {
		return Device{}, err
	}
	if currentChannel.Protocol != channel.Protocol {
		return Device{}, fmt.Errorf("%w: %s -> %s", ErrCrossProtocolMove, currentChannel.Protocol, channel.Protocol)
	}
	if err := validateDeviceForProtocol(input, channel.Protocol); err != nil {
		return Device{}, err
	}
	if strings.TrimSpace(input.ExternalID) == "" {
		input.ExternalID = current.ExternalID
	}
	if err := s.validateDeviceScript(ctx, input.ScriptID); err != nil {
		return Device{}, err
	}
	return s.createDevice(ctx, meta, input, channel.Protocol, id)
}

func (s *Service) createDevice(ctx context.Context, meta AuditMetadata, input DeviceInput, protocol string, id int64) (Device, error) {
	exists, err := s.deviceAddressExists(ctx, input, protocol, id)
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

// BindDeviceScript changes only the optional script identity. It is also used
// by the dedicated binding routes so callers do not have to resend the whole
// acquisition device document. A nil script ID explicitly unbinds the device.
func (s *Service) BindDeviceScript(ctx context.Context, meta AuditMetadata, deviceID int64, scriptID *int64) error {
	if deviceID < 1 {
		return ErrInvalid
	}
	if _, err := s.store.FindDevice(ctx, deviceID); err != nil {
		return err
	}
	if err := s.validateDeviceScript(ctx, scriptID); err != nil {
		return err
	}
	action := "acquisition.device.script.bind"
	if scriptID == nil {
		action = "acquisition.device.script.unbind"
	}
	err := s.store.BindDeviceScript(ctx, deviceID, scriptID, auditEvent(meta, action, "采集设备脚本绑定", deviceID))
	if err == nil {
		s.notifyRuntime(ctx)
	}
	return err
}

func (s *Service) PageScripts(ctx context.Context, query ScriptQuery) (Page[ScriptView], error) {
	page, err := s.store.PageScripts(ctx, query)
	if err != nil {
		return Page[ScriptView]{Page: page.Page, PageSize: page.PageSize, Total: page.Total}, err
	}
	views := make([]ScriptView, 0, len(page.Records))
	for index := range page.Records {
		view, err := s.scriptView(ctx, page.Records[index])
		if err != nil {
			return Page[ScriptView]{Page: page.Page, PageSize: page.PageSize, Total: page.Total}, err
		}
		views = append(views, view)
	}
	return Page[ScriptView]{Records: views, Total: page.Total, Page: page.Page, PageSize: page.PageSize}, nil
}

func (s *Service) FindScript(ctx context.Context, id int64) (*ScriptView, error) {
	if id < 1 {
		return nil, ErrInvalid
	}
	script, err := s.store.FindScript(ctx, id)
	if err != nil {
		return nil, err
	}
	view, err := s.scriptView(ctx, *script)
	if err != nil {
		return nil, err
	}
	return &view, nil
}

func (s *Service) CreateScript(ctx context.Context, meta AuditMetadata, input ScriptInput) (ScriptView, error) {
	input, err := normalizeScriptInput(input)
	if err != nil {
		return ScriptView{}, err
	}
	if err := s.ensureScriptNameAvailable(ctx, input.Name, 0); err != nil {
		return ScriptView{}, err
	}
	value, err := s.store.CreateScript(ctx, Script{Name: input.Name, Description: input.Description, DraftSource: input.DraftSource}, auditEvent(meta, "acquisition.script.create", "采集协议脚本", 0))
	if err != nil {
		return ScriptView{}, err
	}
	return s.scriptView(ctx, value)
}

func (s *Service) UpdateScript(ctx context.Context, meta AuditMetadata, id int64, input ScriptInput) (ScriptView, error) {
	if id < 1 {
		return ScriptView{}, ErrInvalid
	}
	input, err := normalizeScriptInput(input)
	if err != nil {
		return ScriptView{}, err
	}
	if _, err := s.store.FindScript(ctx, id); err != nil {
		return ScriptView{}, err
	}
	if err := s.ensureScriptNameAvailable(ctx, input.Name, id); err != nil {
		return ScriptView{}, err
	}
	value, err := s.store.UpdateScript(ctx, Script{ID: id, Name: input.Name, Description: input.Description, DraftSource: input.DraftSource}, auditEvent(meta, "acquisition.script.update", "采集协议脚本", id))
	if err != nil {
		return ScriptView{}, err
	}
	return s.scriptView(ctx, value)
}

func (s *Service) DeleteScript(ctx context.Context, meta AuditMetadata, id int64) error {
	if id < 1 {
		return ErrInvalid
	}
	if _, err := s.store.FindScript(ctx, id); err != nil {
		return err
	}
	return s.store.DeleteScript(ctx, id, auditEvent(meta, "acquisition.script.delete", "采集协议脚本", id))
}

func (s *Service) ValidateScript(ctx context.Context, id int64) (ScriptValidationResult, error) {
	if id < 1 {
		return ScriptValidationResult{}, ErrInvalid
	}
	script, err := s.store.FindScript(ctx, id)
	if err != nil {
		return ScriptValidationResult{}, err
	}
	return s.validateSource(ctx, script.DraftSource)
}

func (s *Service) PublishScript(ctx context.Context, meta AuditMetadata, id int64) (ScriptVersion, error) {
	if id < 1 {
		return ScriptVersion{}, ErrInvalid
	}
	if _, err := s.store.FindScript(ctx, id); err != nil {
		return ScriptVersion{}, err
	}
	if s.validator == nil {
		return ScriptVersion{}, ErrScriptValidatorUnavailable
	}
	validate := func(source string) error {
		result, err := s.validateSource(ctx, source)
		if err != nil {
			return err
		}
		if !result.Valid {
			return &ScriptValidationFailedError{Result: result}
		}
		return nil
	}
	version, err := s.store.PublishScript(ctx, id, validate, auditEvent(meta, "acquisition.script.publish", "采集协议脚本", id))
	if err == nil {
		s.notifyRuntime(ctx)
	}
	return version, err
}

func (s *Service) ListScriptVersions(ctx context.Context, id int64) ([]ScriptVersion, error) {
	if id < 1 {
		return nil, ErrInvalid
	}
	return s.store.ListScriptVersions(ctx, id)
}

func (s *Service) RollbackScript(ctx context.Context, meta AuditMetadata, id, versionID int64) error {
	if id < 1 || versionID < 1 {
		return ErrInvalid
	}
	err := s.store.RollbackScript(ctx, id, versionID, auditEvent(meta, "acquisition.script.rollback", "采集协议脚本", id))
	if err == nil {
		s.notifyRuntime(ctx)
	}
	return err
}

func (s *Service) ListScriptRuntimeStates(ctx context.Context) ([]ScriptRuntimeState, error) {
	if s.runtimeStates == nil {
		return []ScriptRuntimeState{}, nil
	}
	values, err := s.runtimeStates.List(ctx)
	if values == nil {
		values = []ScriptRuntimeState{}
	}
	return values, err
}

func (s *Service) FindScriptRuntimeState(ctx context.Context, deviceID int64) (*ScriptRuntimeState, error) {
	if deviceID < 1 {
		return nil, ErrInvalid
	}
	if s.runtimeStates == nil {
		return nil, ErrNotFound
	}
	state, err := s.runtimeStates.Get(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, ErrNotFound
	}
	return state, nil
}

func (s *Service) EnabledConfiguration(ctx context.Context) ([]Channel, []Device, error) {
	return s.store.EnabledConfiguration(ctx)
}

type AuditEvent = audit.Event
type AuditMetadata = audit.Metadata

func validateChannel(input ChannelInput) error {
	if strings.TrimSpace(input.Name) == "" || !isSupportedProtocol(input.Protocol) || input.TimeoutMS <= 0 || input.InterRequestDelayMS < 0 || input.InterRequestDelayMS > 60000 || (input.Enabled != Enabled && input.Enabled != Disabled) {
		return ErrInvalid
	}
	switch input.Protocol {
	case ProtocolModbusRTU:
		if input.SerialConfig == nil || !validSerialConfig(*input.SerialConfig) {
			return ErrInvalid
		}
	default:
		if input.SerialConfig != nil {
			return ErrInvalid
		}
	}
	return nil
}

func validateDevice(input DeviceInput) error {
	if strings.TrimSpace(input.Name) == "" || input.ChannelID < 1 || input.PollIntervalMS <= 0 || input.FailureThreshold <= 0 || (input.Enabled != Enabled && input.Enabled != Disabled) {
		return ErrInvalid
	}
	if input.ExternalID != "" && !validExternalID(input.ExternalID) {
		return ErrInvalid
	}
	if input.DeviceType != DeviceTypeFeedProtector {
		return ErrUnsupportedDevice
	}
	if err := validateRegisterBlocks(input.RegisterBlocks); err != nil {
		return err
	}
	return nil
}

func validateDeviceForProtocol(input DeviceInput, protocol string) error {
	if err := validateDevice(input); err != nil {
		return err
	}
	switch protocol {
	case ProtocolModbusRTU:
		if input.UnitID < 1 || input.UnitID > 247 || input.NetworkEndpoint != nil {
			return ErrInvalid
		}
	case ProtocolModbusRTUOverUDP:
		if input.UnitID < 1 || input.UnitID > 247 || input.NetworkEndpoint == nil || !validNetworkEndpoint(*input.NetworkEndpoint) {
			return ErrInvalid
		}
	case ProtocolModbusTCP, ProtocolModbusUDP:
		if input.NetworkEndpoint == nil || !validNetworkEndpoint(*input.NetworkEndpoint) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func validSerialConfig(config SerialConfig) bool {
	config.Parity = strings.ToUpper(strings.TrimSpace(config.Parity))
	return strings.TrimSpace(config.Port) != "" && config.BaudRate > 0 &&
		(config.DataBits == 7 || config.DataBits == 8) &&
		(config.StopBits == 1 || config.StopBits == 2) &&
		(config.Parity == "N" || config.Parity == "E" || config.Parity == "O")
}

func validNetworkEndpoint(endpoint NetworkEndpoint) bool {
	return strings.TrimSpace(endpoint.Host) != "" && endpoint.Port >= 1 && endpoint.Port <= 65535
}

const maxScriptSourceBytes = 262144

func normalizeScriptInput(input ScriptInput) (ScriptInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || !utf8.ValidString(input.Name) || len([]rune(input.Name)) > 100 || !utf8.ValidString(input.Description) || !utf8.ValidString(input.DraftSource) || len([]byte(input.DraftSource)) > maxScriptSourceBytes {
		return ScriptInput{}, ErrInvalid
	}
	return input, nil
}

func (s *Service) ensureScriptNameAvailable(ctx context.Context, name string, excludeID int64) error {
	exists, err := s.store.ScriptNameExists(ctx, name, excludeID)
	if err != nil {
		return err
	}
	if exists {
		return ErrConflict
	}
	return nil
}

func (s *Service) validateDeviceScript(ctx context.Context, scriptID *int64) error {
	if scriptID == nil {
		return nil
	}
	if *scriptID < 1 {
		return ErrInvalid
	}
	script, err := s.store.FindScript(ctx, *scriptID)
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

func (s *Service) validateSource(ctx context.Context, source string) (ScriptValidationResult, error) {
	if s.validator == nil {
		return ScriptValidationResult{}, ErrScriptValidatorUnavailable
	}
	result, err := s.validator.Validate(ctx, source)
	if err != nil {
		return ScriptValidationResult{}, err
	}
	if result.Errors == nil {
		result.Errors = []ScriptValidationError{}
	}
	if len(result.Errors) > 0 {
		result.Valid = false
	}
	if !result.Valid && len(result.Errors) == 0 {
		result.Errors = []ScriptValidationError{{Message: "脚本校验未通过"}}
	}
	return result, nil
}

func (s *Service) scriptView(ctx context.Context, value Script) (ScriptView, error) {
	view := ScriptView{
		ID:                 value.ID,
		Name:               value.Name,
		Description:        value.Description,
		DraftSource:        value.DraftSource,
		PublishedVersionID: value.PublishedVersionID,
		CreateTime:         value.CreateTime,
		UpdateTime:         value.UpdateTime,
	}
	if value.PublishedVersionID != nil {
		version, err := s.store.FindScriptVersion(ctx, value.ID, *value.PublishedVersionID)
		if err != nil {
			return ScriptView{}, err
		}
		view.PublishedVersion = version
		checksum := sha256.Sum256([]byte(value.DraftSource))
		view.DraftMatchesPublished = hex.EncodeToString(checksum[:]) == version.Checksum
	}
	count, err := s.store.CountDevicesByScript(ctx, value.ID)
	if err != nil {
		return ScriptView{}, err
	}
	view.BoundDeviceCount = count
	return view, nil
}

func isSupportedProtocol(protocol string) bool {
	switch protocol {
	case ProtocolModbusRTU, ProtocolModbusTCP, ProtocolModbusUDP, ProtocolModbusRTUOverUDP:
		return true
	default:
		return false
	}
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
	var serialConfig *SerialConfig
	if input.SerialConfig != nil {
		value := *input.SerialConfig
		value.Port = strings.TrimSpace(value.Port)
		value.Parity = strings.ToUpper(strings.TrimSpace(value.Parity))
		serialConfig = &value
	}
	return Channel{ID: id, Name: strings.TrimSpace(input.Name), Protocol: input.Protocol, SerialConfig: serialConfig, TimeoutMS: input.TimeoutMS, InterRequestDelayMS: input.InterRequestDelayMS, Enabled: input.Enabled}
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
	var endpoint *NetworkEndpoint
	if input.NetworkEndpoint != nil {
		value := *input.NetworkEndpoint
		value.Host = strings.TrimSpace(value.Host)
		endpoint = &value
	}
	externalID := strings.TrimSpace(input.ExternalID)
	if externalID == "" && id == 0 {
		externalID = newExternalID()
	}
	return Device{ID: id, ExternalID: externalID, Name: strings.TrimSpace(input.Name), DeviceType: strings.TrimSpace(input.DeviceType), ChannelID: input.ChannelID, UnitID: input.UnitID, ScriptID: input.ScriptID, NetworkEndpoint: endpoint, PollIntervalMS: input.PollIntervalMS, FailureThreshold: input.FailureThreshold, Enabled: input.Enabled, RegisterBlocks: blocks}
}

func validExternalID(value string) bool {
	return len([]byte(value)) <= 128 && value != "" && !strings.ContainsAny(value, "/+#\x00\r\n")
}

func newExternalID() string {
	bytes := make([]byte, 12)
	if _, err := cryptorand.Read(bytes); err == nil {
		return "device-" + hex.EncodeToString(bytes)
	}
	return fmt.Sprintf("device-%d", time.Now().UTC().UnixNano())
}

func (s *Service) deviceAddressExists(ctx context.Context, input DeviceInput, protocol string, excludeID int64) (bool, error) {
	if protocol == ProtocolModbusRTU {
		return s.store.UnitIDExists(ctx, input.ChannelID, input.UnitID, excludeID)
	}
	if input.NetworkEndpoint == nil {
		return false, ErrInvalid
	}
	return s.store.NetworkEndpointExists(ctx, input.ChannelID, input.NetworkEndpoint.Host, input.NetworkEndpoint.Port, input.UnitID, excludeID)
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
