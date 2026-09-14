package acquisition

import (
	"context"
	"fmt"
)

// FeedProtectorData is the business representation exposed by the first
// phase. Modbus-specific register types stay in this package and never cross
// into the HTTP DTO boundary.
type FeedProtectorData struct {
	Voltage     float64 `json:"voltage"`
	Current     float64 `json:"current"`
	ActivePower float64 `json:"activePower"`
	Frequency   float64 `json:"frequency"`
	PowerFactor float64 `json:"powerFactor"`
	Status      uint16  `json:"status"`
}

const feedProtectorRegisterCount = 7

type RegisterReader interface {
	ReadHoldingRegisters(context.Context, uint8, uint16, uint16) ([]uint16, error)
}

// ReadFeedProtector keeps the two register regions separate so a status
// timeout does not discard successfully read electrical values.
func ReadFeedProtector(ctx context.Context, reader RegisterReader, slaveID uint8) (FeedProtectorReading, error) {
	if reader == nil {
		return FeedProtectorReading{}, fmt.Errorf("寄存器读取器不能为空")
	}

	electrical, err := reader.ReadHoldingRegisters(ctx, slaveID, 0, 6)
	if err != nil {
		return FeedProtectorReading{}, fmt.Errorf("读取馈电保护器电气数据失败: %w", err)
	}
	if len(electrical) < 6 {
		return FeedProtectorReading{}, fmt.Errorf("馈电保护器电气数据响应不足")
	}

	registers := append(append([]uint16(nil), electrical[:6]...), 0)
	data, err := ParseFeedProtectorRegisters(registers)
	if err != nil {
		return FeedProtectorReading{}, err
	}
	reading := FeedProtectorReading{
		Data: data,
		ValidFields: map[string]bool{
			"voltage":     true,
			"current":     true,
			"activePower": true,
			"frequency":   true,
			"powerFactor": true,
		},
	}

	status, err := reader.ReadHoldingRegisters(ctx, slaveID, 6, 1)
	if err != nil {
		return reading, fmt.Errorf("读取馈电保护器状态失败: %w", err)
	}
	if len(status) < 1 {
		return reading, fmt.Errorf("馈电保护器状态响应为空")
	}
	reading.Data.Status = status[0]
	reading.ValidFields["status"] = true
	return reading, nil
}

// ParseFeedProtectorRegisters converts the fixed first-phase register block
// into business values. The repository currently contains no manufacturer
// register table, so this compact map is the replaceable protocol seam for
// the development loop; the confirmed vendor addresses/scales must replace
// only this adapter before physical-device acceptance.
func ParseFeedProtectorRegisters(registers []uint16) (FeedProtectorData, error) {
	if len(registers) < feedProtectorRegisterCount {
		return FeedProtectorData{}, fmt.Errorf("馈电保护器寄存器响应不足：得到 %d 个，需要至少 %d 个", len(registers), feedProtectorRegisterCount)
	}

	activePowerRaw := uint32(registers[2])<<16 | uint32(registers[3])
	return FeedProtectorData{
		Voltage:     float64(registers[0]) / 10,
		Current:     float64(registers[1]) / 10,
		ActivePower: float64(activePowerRaw) / 10,
		Frequency:   float64(registers[4]) / 100,
		PowerFactor: float64(registers[5]) / 1000,
		Status:      registers[6],
	}, nil
}
