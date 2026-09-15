package acquisition

import (
	"context"
	"fmt"
)

// RegisterReader reads the two read-only Modbus register spaces used by the
// acquisition runtime.
type RegisterReader interface {
	ReadHoldingRegisters(context.Context, uint8, uint16, uint16) ([]uint16, error)
	ReadInputRegisters(context.Context, uint8, uint16, uint16) ([]uint16, error)
}

func readRegisterBlock(ctx context.Context, reader RegisterReader, slaveID uint8, block RegisterBlock) ([]uint16, error) {
	if reader == nil {
		return nil, fmt.Errorf("寄存器读取器不能为空")
	}
	if block.StartAddress < 0 || block.StartAddress > 65535 || block.Quantity < 1 || block.Quantity > 125 || block.StartAddress+block.Quantity-1 > 65535 {
		return nil, fmt.Errorf("读取块参数超出范围")
	}
	address := uint16(block.StartAddress)
	quantity := uint16(block.Quantity)
	var (
		registers []uint16
		err       error
	)
	switch block.FunctionCode {
	case FunctionCodeReadHoldingRegisters:
		registers, err = reader.ReadHoldingRegisters(ctx, slaveID, address, quantity)
	case FunctionCodeReadInputRegisters:
		registers, err = reader.ReadInputRegisters(ctx, slaveID, address, quantity)
	default:
		return nil, fmt.Errorf("不支持的 Modbus 功能码: %d", block.FunctionCode)
	}
	if err != nil {
		return nil, err
	}
	if len(registers) != block.Quantity {
		return nil, fmt.Errorf("读取块响应数量不符：得到 %d 个，需要 %d 个", len(registers), block.Quantity)
	}
	return append([]uint16(nil), registers...), nil
}
