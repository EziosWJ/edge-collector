package acquisition

import (
	"context"
	"errors"
	"testing"
)

func TestReadRegisterBlockFC03(t *testing.T) {
	reader := fakeRegisterReader{
		holding: map[registerRequest][]uint16{{address: 10, quantity: 2}: {0x1234, 0xABCD}},
	}
	values, err := readRegisterBlock(context.Background(), reader, 3, RegisterBlock{
		FunctionCode: FunctionCodeReadHoldingRegisters,
		StartAddress: 10,
		Quantity:     2,
	})
	if err != nil {
		t.Fatalf("readRegisterBlock() error = %v", err)
	}
	if len(values) != 2 || values[0] != 0x1234 || values[1] != 0xABCD {
		t.Fatalf("values = %#v, want raw FC03 values", values)
	}
}

func TestReadRegisterBlockFC04(t *testing.T) {
	reader := fakeRegisterReader{
		input: map[registerRequest][]uint16{{address: 20, quantity: 1}: {0x4321}},
	}
	values, err := readRegisterBlock(context.Background(), reader, 3, RegisterBlock{
		FunctionCode: FunctionCodeReadInputRegisters,
		StartAddress: 20,
		Quantity:     1,
	})
	if err != nil {
		t.Fatalf("readRegisterBlock() error = %v", err)
	}
	if len(values) != 1 || values[0] != 0x4321 {
		t.Fatalf("values = %#v, want raw FC04 value", values)
	}
}

func TestReadRegisterBlockRejectsShortResponse(t *testing.T) {
	reader := fakeRegisterReader{
		holding: map[registerRequest][]uint16{{address: 0, quantity: 2}: {1}},
	}
	if _, err := readRegisterBlock(context.Background(), reader, 1, RegisterBlock{FunctionCode: 3, Quantity: 2}); err == nil {
		t.Fatal("readRegisterBlock() error = nil, want short response error")
	}
}

func TestReadRegisterBlockPropagatesFailure(t *testing.T) {
	reader := fakeRegisterReader{
		errors: map[registerRequest]error{{address: 0, quantity: 1}: errors.New("timeout")},
	}
	if _, err := readRegisterBlock(context.Background(), reader, 1, RegisterBlock{FunctionCode: 3, Quantity: 1}); err == nil {
		t.Fatal("readRegisterBlock() error = nil, want transport error")
	}
}

type registerRequest struct {
	address  uint16
	quantity uint16
}

type fakeRegisterReader struct {
	holding map[registerRequest][]uint16
	input   map[registerRequest][]uint16
	errors  map[registerRequest]error
}

func (f fakeRegisterReader) ReadHoldingRegisters(_ context.Context, _ uint8, address, quantity uint16) ([]uint16, error) {
	request := registerRequest{address: address, quantity: quantity}
	if err := f.errors[request]; err != nil {
		return nil, err
	}
	return f.holding[request], nil
}

func (f fakeRegisterReader) ReadInputRegisters(_ context.Context, _ uint8, address, quantity uint16) ([]uint16, error) {
	request := registerRequest{address: address, quantity: quantity}
	if err := f.errors[request]; err != nil {
		return nil, err
	}
	return f.input[request], nil
}
