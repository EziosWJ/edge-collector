package acquisition

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/simonvetter/modbus"
)

// ModbusWriter is the optional write seam used by dynamic scripts. The
// existing ModbusSession intentionally remains read-only so existing session
// adapters and tests do not need to implement write operations. The session
// has already selected the device unit ID for the current cycle; the explicit
// slaveID keeps the adapter compatible with RegisterReader and lets a fake
// verify the target unit.
type ModbusWriter interface {
	WriteRegisters(context.Context, uint8, uint16, []uint16) error
	WriteCoil(context.Context, uint8, uint16, bool) error
}

var ErrModbusWriterUnavailable = errors.New("动态 Modbus 写入能力不可用")

type modbusSession struct {
	client *modbus.ModbusClient
}

func NewModbusSessionFactory() SessionFactory {
	return func(channel Channel, device Device) (ModbusSession, error) {
		return newModbusSession(channel, device)
	}
}

func newModbusSession(channel Channel, device Device) (ModbusSession, error) {
	timeout := time.Duration(channel.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 300 * time.Millisecond
	}
	config := &modbus.ClientConfiguration{Timeout: timeout, Logger: log.New(io.Discard, "", 0)}
	switch channel.Protocol {
	case "", ProtocolModbusRTU:
		if channel.SerialConfig == nil {
			return nil, fmt.Errorf("串口通道配置不能为空")
		}
		serial := channel.SerialConfig
		parity, err := modbusParity(serial.Parity)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(serial.Port) == "" {
			return nil, fmt.Errorf("串口设备不能为空")
		}
		config.URL = "rtu://" + serial.Port
		config.Speed = uint(serial.BaudRate)
		config.DataBits = uint(serial.DataBits)
		config.Parity = parity
		config.StopBits = uint(serial.StopBits)
	case ProtocolModbusTCP, ProtocolModbusUDP, ProtocolModbusRTUOverUDP:
		if device.NetworkEndpoint == nil || strings.TrimSpace(device.NetworkEndpoint.Host) == "" || device.NetworkEndpoint.Port < 1 || device.NetworkEndpoint.Port > 65535 {
			return nil, fmt.Errorf("网络设备 endpoint 配置不能为空")
		}
		host := strings.TrimSpace(device.NetworkEndpoint.Host)
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
		scheme := map[string]string{ProtocolModbusTCP: "tcp", ProtocolModbusUDP: "udp", ProtocolModbusRTUOverUDP: "rtuoverudp"}[channel.Protocol]
		config.URL = scheme + "://" + net.JoinHostPort(host, strconv.Itoa(device.NetworkEndpoint.Port))
	default:
		return nil, fmt.Errorf("不支持的 Modbus 协议: %s", channel.Protocol)
	}
	client, err := modbus.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("创建 Modbus 客户端失败: %w", err)
	}
	return &modbusSession{client: client}, nil
}

func (s *modbusSession) Open() error { return s.client.Open() }

func (s *modbusSession) Close() error { return s.client.Close() }

func (s *modbusSession) SetUnitID(id uint8) error { return s.client.SetUnitId(id) }

func (s *modbusSession) ReadHoldingRegisters(_ context.Context, _ uint8, address, quantity uint16) ([]uint16, error) {
	return s.client.ReadRegisters(address, quantity, modbus.HOLDING_REGISTER)
}

func (s *modbusSession) ReadInputRegisters(_ context.Context, _ uint8, address, quantity uint16) ([]uint16, error) {
	return s.client.ReadRegisters(address, quantity, modbus.INPUT_REGISTER)
}

func (s *modbusSession) WriteRegisters(_ context.Context, _ uint8, address uint16, values []uint16) error {
	return s.client.WriteRegisters(address, append([]uint16(nil), values...))
}

func (s *modbusSession) WriteCoil(_ context.Context, _ uint8, address uint16, on bool) error {
	return s.client.WriteCoil(address, on)
}

func modbusParity(value string) (uint, error) {
	switch value {
	case "", "N", "n":
		return modbus.PARITY_NONE, nil
	case "E", "e":
		return modbus.PARITY_EVEN, nil
	case "O", "o":
		return modbus.PARITY_ODD, nil
	default:
		return 0, fmt.Errorf("不支持的串口校验方式: %s", value)
	}
}

func isModbusExceptionError(err error) bool {
	for _, exception := range []error{
		modbus.ErrIllegalFunction,
		modbus.ErrIllegalDataAddress,
		modbus.ErrIllegalDataValue,
		modbus.ErrServerDeviceFailure,
		modbus.ErrAcknowledge,
		modbus.ErrServerDeviceBusy,
		modbus.ErrMemoryParityError,
		modbus.ErrGWPathUnavailable,
		modbus.ErrGWTargetFailedToRespond,
	} {
		if errors.Is(err, exception) {
			return true
		}
	}
	return false
}
