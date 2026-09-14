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
