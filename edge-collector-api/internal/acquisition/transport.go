package acquisition

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/simonvetter/modbus"
)

type modbusSession struct {
	client *modbus.ModbusClient
}

func NewModbusSessionFactory() SessionFactory {
	return func(channel Channel) (ModbusSession, error) {
		return newModbusSession(channel)
	}
}

func newModbusSession(channel Channel) (ModbusSession, error) {
	parity, err := modbusParity(channel.Parity)
	if err != nil {
		return nil, err
	}
	if channel.Port == "" {
		return nil, fmt.Errorf("串口设备不能为空")
	}
	timeout := time.Duration(channel.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 300 * time.Millisecond
	}
	client, err := modbus.NewClient(&modbus.ClientConfiguration{
		URL:      "rtu://" + channel.Port,
		Speed:    uint(channel.BaudRate),
		DataBits: uint(channel.DataBits),
		Parity:   parity,
		StopBits: uint(channel.StopBits),
		Timeout:  timeout,
		Logger:   log.New(io.Discard, "", 0),
	})
	if err != nil {
		return nil, fmt.Errorf("创建 Modbus RTU 客户端失败: %w", err)
	}
	return &modbusSession{client: client}, nil
}

func (s *modbusSession) Open() error { return s.client.Open() }

func (s *modbusSession) Close() error { return s.client.Close() }

func (s *modbusSession) SetUnitID(id uint8) error { return s.client.SetUnitId(id) }

func (s *modbusSession) ReadHoldingRegisters(_ context.Context, _ uint8, address, quantity uint16) ([]uint16, error) {
	return s.client.ReadRegisters(address, quantity, modbus.HOLDING_REGISTER)
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
