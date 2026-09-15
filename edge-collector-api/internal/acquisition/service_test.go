package acquisition

import "testing"

func TestValidateChannelInterRequestDelayRange(t *testing.T) {
	base := ChannelInput{
		Name:      "RS485",
		Port:      "/dev/ttyUSB0",
		BaudRate:  19200,
		DataBits:  8,
		StopBits:  1,
		Parity:    "N",
		TimeoutMS: 300,
		Enabled:   Enabled,
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
