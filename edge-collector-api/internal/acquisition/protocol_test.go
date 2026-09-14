package acquisition

import (
	"context"
	"errors"
	"testing"
)

func TestParseFeedProtectorRegisters(t *testing.T) {
	registers := []uint16{
		3000,   // voltage: 300.0 V
		125,    // current: 12.5 A
		0x0000, // active power high word
		0x04D2, // active power low word: 123.4 kW
		5000,   // frequency: 50.00 Hz
		980,    // power factor: 0.980
		1,      // status
	}

	data, err := ParseFeedProtectorRegisters(registers)
	if err != nil {
		t.Fatalf("ParseFeedProtectorRegisters() error = %v", err)
	}

	if data.Voltage != 300 {
		t.Errorf("Voltage = %v, want 300", data.Voltage)
	}
	if data.Current != 12.5 {
		t.Errorf("Current = %v, want 12.5", data.Current)
	}
	if data.ActivePower != 123.4 {
		t.Errorf("ActivePower = %v, want 123.4", data.ActivePower)
	}
	if data.Frequency != 50 {
		t.Errorf("Frequency = %v, want 50", data.Frequency)
	}
	if data.PowerFactor != 0.98 {
		t.Errorf("PowerFactor = %v, want 0.98", data.PowerFactor)
	}
	if data.Status != 1 {
		t.Errorf("Status = %v, want 1", data.Status)
	}
}

func TestParseFeedProtectorRegistersRejectsShortResponse(t *testing.T) {
	if _, err := ParseFeedProtectorRegisters([]uint16{3000}); err == nil {
		t.Fatal("ParseFeedProtectorRegisters() error = nil, want short response error")
	}
}

func TestReadFeedProtectorRetainsSuccessfulRegionWhenStatusReadFails(t *testing.T) {
	reader := fakeRegisterReader{
		blocks: map[registerRequest][]uint16{
			{address: 0, quantity: 6}: {3000, 125, 0, 1234, 5000, 980},
		},
		errors: map[registerRequest]error{
			{address: 6, quantity: 1}: errors.New("status timeout"),
		},
	}

	reading, err := ReadFeedProtector(context.Background(), reader, 3)
	if err == nil {
		t.Fatal("ReadFeedProtector() error = nil, want partial read error")
	}
	if reading.Data.Voltage != 300 || !reading.ValidFields["voltage"] {
		t.Fatalf("reading = %#v, want successful electrical region", reading)
	}
	if reading.ValidFields["status"] {
		t.Fatal("status validity = true, want false")
	}
}

type registerRequest struct {
	address  uint16
	quantity uint16
}

type fakeRegisterReader struct {
	blocks map[registerRequest][]uint16
	errors map[registerRequest]error
}

func (f fakeRegisterReader) ReadHoldingRegisters(_ context.Context, _ uint8, address, quantity uint16) ([]uint16, error) {
	request := registerRequest{address: address, quantity: quantity}
	if err := f.errors[request]; err != nil {
		return nil, err
	}
	return f.blocks[request], nil
}
