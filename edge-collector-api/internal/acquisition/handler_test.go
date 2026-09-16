package acquisition

import (
	"encoding/json"
	"testing"
)

func TestAcquisitionDTOUsesTransportNeutralFields(t *testing.T) {
	var channel channelRequest
	if err := json.Unmarshal([]byte(`{"name":"TCP 通道","protocol":"MODBUS_TCP","timeoutMs":500,"interRequestDelayMs":10,"enabled":1}`), &channel); err != nil {
		t.Fatal(err)
	}
	channelInput := channel.input()
	if channelInput.Protocol != ProtocolModbusTCP || channelInput.SerialConfig != nil {
		t.Fatalf("channel input = %#v, want TCP without serial config", channelInput)
	}

	var device deviceRequest
	if err := json.Unmarshal([]byte(`{"name":"网络设备","deviceType":"GENERIC","channelId":3,"unitId":0,"networkEndpoint":{"host":"[2001:db8::1]","port":1502},"pollIntervalMs":1000,"failureThreshold":3,"enabled":1}`), &device); err != nil {
		t.Fatal(err)
	}
	deviceInput := device.input()
	if deviceInput.UnitID != 0 || deviceInput.NetworkEndpoint == nil || deviceInput.NetworkEndpoint.Port != 1502 {
		t.Fatalf("device input = %#v, want unit ID 0 and network endpoint", deviceInput)
	}
}
