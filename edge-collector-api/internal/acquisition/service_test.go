package acquisition

import (
	"context"
	"errors"
	"testing"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
)

func TestValidateChannelInterRequestDelayRange(t *testing.T) {
	base := ChannelInput{
		Name:         "RS485",
		Protocol:     ProtocolModbusRTU,
		SerialConfig: &SerialConfig{Port: "/dev/ttyUSB0", BaudRate: 19200, DataBits: 8, StopBits: 1, Parity: "N"},
		TimeoutMS:    300,
		Enabled:      Enabled,
	}

	for _, test := range []struct {
		name  string
		delay int
		want  error
	}{
		{name: "zero is allowed", delay: 0, want: nil},
		{name: "maximum is allowed", delay: 60000, want: nil},
		{name: "negative is rejected", delay: -1, want: ErrInvalid},
		{name: "above maximum is rejected", delay: 60001, want: ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := base
			input.InterRequestDelayMS = test.delay
			if err := validateChannel(input); err != test.want {
				t.Fatalf("validateChannel() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateRegisterBlocks(t *testing.T) {
	if err := validateRegisterBlocks(nil); err != nil {
		t.Fatalf("validateRegisterBlocks() error = %v, want empty configuration to be allowed", err)
	}

	base := []RegisterBlockInput{
		{Name: "保持寄存器", FunctionCode: FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 2, SortOrder: 0},
		{Name: "输入寄存器", FunctionCode: FunctionCodeReadInputRegisters, StartAddress: 0, Quantity: 1, SortOrder: 1},
	}
	if err := validateRegisterBlocks(base); err != nil {
		t.Fatalf("validateRegisterBlocks() error = %v, want valid FC03/FC04 ranges", err)
	}

	tests := []struct {
		name   string
		blocks []RegisterBlockInput
	}{
		{name: "unsupported function code", blocks: []RegisterBlockInput{{Name: "块", FunctionCode: 6, Quantity: 1}}},
		{name: "quantity too large", blocks: []RegisterBlockInput{{Name: "块", FunctionCode: 3, Quantity: 126}}},
		{name: "address overflow", blocks: []RegisterBlockInput{{Name: "块", FunctionCode: 3, StartAddress: 65535, Quantity: 2}}},
		{name: "duplicate name", blocks: []RegisterBlockInput{{Name: "块", FunctionCode: 3, Quantity: 1}, {Name: "块", FunctionCode: 4, Quantity: 1}}},
		{name: "same function overlap", blocks: []RegisterBlockInput{{Name: "块一", FunctionCode: 3, StartAddress: 0, Quantity: 2}, {Name: "块二", FunctionCode: 3, StartAddress: 1, Quantity: 1}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateRegisterBlocks(test.blocks); err != ErrInvalid {
				t.Fatalf("validateRegisterBlocks() error = %v, want %v", err, ErrInvalid)
			}
		})
	}
}

func TestValidateChannelTransportConfiguration(t *testing.T) {
	base := ChannelInput{
		Name:     "采集通道",
		Protocol: ProtocolModbusRTU,
		SerialConfig: &SerialConfig{
			Port:     "/dev/ttyUSB0",
			BaudRate: 19200,
			DataBits: 8,
			StopBits: 1,
			Parity:   "N",
		},
		TimeoutMS:           300,
		InterRequestDelayMS: 25,
		Enabled:             Enabled,
	}

	if err := validateChannel(base); err != nil {
		t.Fatalf("valid RTU channel error = %v", err)
	}

	for _, test := range []struct {
		name  string
		input ChannelInput
	}{
		{name: "unknown protocol", input: func() ChannelInput { value := base; value.Protocol = "MODBUS_UNKNOWN"; return value }()},
		{name: "RTU without serial config", input: func() ChannelInput { value := base; value.SerialConfig = nil; return value }()},
		{name: "network with serial config", input: func() ChannelInput { value := base; value.Protocol = ProtocolModbusTCP; return value }()},
		{name: "network without serial config", input: func() ChannelInput {
			value := base
			value.Protocol = ProtocolModbusUDP
			value.SerialConfig = nil
			return value
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateChannel(test.input)
			if test.name == "network without serial config" {
				if err != nil {
					t.Fatalf("validateChannel() error = %v, want nil", err)
				}
				return
			}
			if err != ErrInvalid {
				t.Fatalf("validateChannel() error = %v, want %v", err, ErrInvalid)
			}
		})
	}
}

func TestServiceRejectsChannelProtocolChange(t *testing.T) {
	store := &transportStoreFake{channel: &Channel{
		ID:       7,
		Name:     "RTU 通道",
		Protocol: ProtocolModbusRTU,
		SerialConfig: &SerialConfig{
			Port:     "/dev/ttyUSB0",
			BaudRate: 19200,
			DataBits: 8,
			StopBits: 1,
			Parity:   "N",
		},
		TimeoutMS: 300,
		Enabled:   Enabled,
	}}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}

	input := validChannelInput(ProtocolModbusTCP)
	if _, err := service.UpdateChannel(context.Background(), AuditMetadata{}, 7, input); err == nil {
		t.Fatal("UpdateChannel() error = nil, want protocol immutability error")
	} else if !isInvalidConfigurationError(err) {
		t.Fatalf("UpdateChannel() error = %v, want invalid configuration error", err)
	}
}

func TestServiceUsesNetworkEndpointForDeviceUniqueness(t *testing.T) {
	store := &transportStoreFake{channel: &Channel{ID: 3, Protocol: ProtocolModbusTCP, Enabled: Enabled}}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}

	input := validDeviceInput()
	input.UnitID = 1
	input.NetworkEndpoint = &NetworkEndpoint{Host: "192.168.1.11", Port: 502}
	created, err := service.CreateDevice(context.Background(), AuditMetadata{}, input)
	if err != nil {
		t.Fatalf("CreateDevice() error = %v", err)
	}
	if created.UnitID != 1 || created.NetworkEndpoint == nil || created.NetworkEndpoint.Host != "192.168.1.11" {
		t.Fatalf("created device = %#v, want network endpoint and unit ID", created)
	}
	if store.networkEndpointCalls != 1 || store.lastHost != "192.168.1.11" || store.lastPort != 502 || store.lastUnitID != 1 {
		t.Fatalf("network uniqueness query = calls %d host %q port %d unit %d", store.networkEndpointCalls, store.lastHost, store.lastPort, store.lastUnitID)
	}
}

func TestValidateDeviceRequiresEndpointForRTUOverUDP(t *testing.T) {
	input := validDeviceInput()
	input.NetworkEndpoint = nil
	if err := validateDeviceForProtocol(input, ProtocolModbusRTUOverUDP); err != ErrInvalid {
		t.Fatalf("validateDeviceForProtocol() error = %v, want %v", err, ErrInvalid)
	}
	input.NetworkEndpoint = &NetworkEndpoint{Host: "127.0.0.1", Port: 1700}
	if err := validateDeviceForProtocol(input, ProtocolModbusRTUOverUDP); err != nil {
		t.Fatalf("validateDeviceForProtocol() error = %v, want valid endpoint", err)
	}
}

func TestServiceRejectsCrossProtocolDeviceMove(t *testing.T) {
	store := &transportStoreFake{
		channel:      &Channel{ID: 3, Protocol: ProtocolModbusTCP, Enabled: Enabled},
		device:       &Device{ID: 9, ChannelID: 2, UnitID: 1, DeviceType: DeviceTypeFeedProtector, Enabled: Enabled},
		channelsByID: map[int64]*Channel{2: {ID: 2, Protocol: ProtocolModbusRTU, Enabled: Enabled}, 3: {ID: 3, Protocol: ProtocolModbusTCP, Enabled: Enabled}},
	}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}

	input := validDeviceInput()
	input.ChannelID = 3
	input.UnitID = 1
	input.NetworkEndpoint = &NetworkEndpoint{Host: "127.0.0.1", Port: 502}
	if _, err := service.UpdateDevice(context.Background(), AuditMetadata{}, 9, input); err == nil {
		t.Fatal("UpdateDevice() error = nil, want cross-protocol move rejection")
	} else if !isInvalidConfigurationError(err) {
		t.Fatalf("UpdateDevice() error = %v, want invalid configuration error", err)
	}
}

func validChannelInput(protocol string) ChannelInput {
	input := ChannelInput{
		Name:                "通道",
		Protocol:            protocol,
		TimeoutMS:           300,
		InterRequestDelayMS: 0,
		Enabled:             Enabled,
	}
	if protocol == ProtocolModbusRTU {
		input.SerialConfig = &SerialConfig{Port: "/dev/ttyUSB0", BaudRate: 19200, DataBits: 8, StopBits: 1, Parity: "N"}
	}
	return input
}

func validDeviceInput() DeviceInput {
	return DeviceInput{
		Name:             "设备",
		DeviceType:       DeviceTypeFeedProtector,
		ChannelID:        3,
		UnitID:           1,
		PollIntervalMS:   1000,
		FailureThreshold: 3,
		Enabled:          Enabled,
	}
}

func isInvalidConfigurationError(err error) bool {
	return errors.Is(err, ErrInvalid) || errors.Is(err, ErrProtocolImmutable) || errors.Is(err, ErrCrossProtocolMove)
}

type transportStoreFake struct {
	channel              *Channel
	channelsByID         map[int64]*Channel
	device               *Device
	networkEndpointCalls int
	unitIDCalls          int
	lastHost             string
	lastPort             int
	lastUnitID           uint8
}

func (f *transportStoreFake) PageChannels(context.Context, ChannelQuery) (Page[Channel], error) {
	return Page[Channel]{}, nil
}

func (f *transportStoreFake) FindChannel(_ context.Context, id int64) (*Channel, error) {
	if f.channelsByID != nil {
		if value, ok := f.channelsByID[id]; ok {
			return value, nil
		}
	}
	if f.channel != nil && f.channel.ID == id {
		return f.channel, nil
	}
	return nil, ErrNotFound
}

func (f *transportStoreFake) CreateChannel(context.Context, Channel, audit.Event) (Channel, error) {
	return Channel{}, nil
}

func (f *transportStoreFake) UpdateChannel(context.Context, Channel, audit.Event) (Channel, error) {
	return Channel{}, nil
}

func (f *transportStoreFake) DeleteChannel(context.Context, int64, audit.Event) error { return nil }

func (f *transportStoreFake) CountDevicesByChannel(context.Context, int64) (int64, error) {
	return 0, nil
}

func (f *transportStoreFake) PageDevices(context.Context, DeviceQuery) (Page[Device], error) {
	return Page[Device]{}, nil
}

func (f *transportStoreFake) FindDevice(_ context.Context, id int64) (*Device, error) {
	if f.device != nil && f.device.ID == id {
		return f.device, nil
	}
	return nil, ErrNotFound
}

func (f *transportStoreFake) UnitIDExists(_ context.Context, _ int64, unitID uint8, _ int64) (bool, error) {
	f.unitIDCalls++
	f.lastUnitID = unitID
	return false, nil
}

func (f *transportStoreFake) NetworkEndpointExists(_ context.Context, _ int64, host string, port int, unitID uint8, _ int64) (bool, error) {
	f.networkEndpointCalls++
	f.lastHost = host
	f.lastPort = port
	f.lastUnitID = unitID
	return false, nil
}

func (f *transportStoreFake) CreateDevice(_ context.Context, value Device, _ audit.Event) (Device, error) {
	if value.ID == 0 {
		value.ID = 10
	}
	return value, nil
}

func (f *transportStoreFake) UpdateDevice(context.Context, Device, audit.Event) (Device, error) {
	return Device{}, nil
}

func (f *transportStoreFake) DeleteDevice(context.Context, int64, audit.Event) error { return nil }

func (f *transportStoreFake) EnabledConfiguration(context.Context) ([]Channel, []Device, error) {
	return nil, nil, nil
}
