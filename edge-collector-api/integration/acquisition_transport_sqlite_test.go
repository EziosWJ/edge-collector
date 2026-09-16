//go:build integration

package integration

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/app"
)

func TestSQLiteAcquisitionTransportAPIUsesEndpointIdentity(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "transport.db")
	runSQLiteMigrations(t, databasePath)
	database := openSQLiteDatabase(t, databasePath)
	defer func() { _ = database.Close() }()
	router, err := app.Build(testAPIConfig(), database, sqliteDependencies(t, database, filepath.Join(directory, "uploads")))
	if err != nil {
		t.Fatal(err)
	}
	token := loginAdmin(t, router)

	rtu := serveJSON(router, http.MethodPost, "/api/v1/acquisition/channels", `{"name":"RTU","protocol":"MODBUS_RTU","serialConfig":{"port":"/dev/ttyUSB0","baudRate":19200,"dataBits":8,"stopBits":1,"parity":"N"},"timeoutMs":300,"interRequestDelayMs":0,"enabled":1}`, token)
	assertEnvelopeCode(t, rtu, http.StatusOK, 200, "success")
	tcp := serveJSON(router, http.MethodPost, "/api/v1/acquisition/channels", `{"name":"TCP","protocol":"MODBUS_TCP","timeoutMs":300,"interRequestDelayMs":0,"enabled":1}`, token)
	assertEnvelopeCode(t, tcp, http.StatusOK, 200, "success")
	if !strings.Contains(rtu.Body.String(), `"protocol":"MODBUS_RTU"`) || !strings.Contains(rtu.Body.String(), `"serialConfig"`) || strings.Contains(rtu.Body.String(), `"slaveId"`) {
		t.Fatalf("RTU channel response does not use transport model: %s", rtu.Body.String())
	}

	device := `{"name":"TCP设备-127","deviceType":"FEED_PROTECTOR","channelId":2,"unitId":1,"networkEndpoint":{"host":"127.0.0.1","port":1502},"pollIntervalMs":1000,"failureThreshold":3,"enabled":1}`
	created := serveJSON(router, http.MethodPost, "/api/v1/acquisition/devices", device, token)
	assertEnvelopeCode(t, created, http.StatusOK, 200, "success")
	if !strings.Contains(created.Body.String(), `"unitId":1`) || !strings.Contains(created.Body.String(), `"networkEndpoint"`) {
		t.Fatalf("network device response does not use endpoint model: %s", created.Body.String())
	}

	secondPayload := strings.Replace(device, "TCP设备-127", "TCP设备-localhost", 1)
	secondPayload = strings.Replace(secondPayload, "127.0.0.1", "localhost", 1)
	second := serveJSON(router, http.MethodPost, "/api/v1/acquisition/devices", secondPayload, token)
	assertEnvelopeCode(t, second, http.StatusOK, 200, "success")
	secondSameEndpoint := serveJSON(router, http.MethodPost, "/api/v1/acquisition/devices", strings.Replace(device, "TCP设备-127", "TCP设备-duplicate", 1), token)
	assertEnvelopeCode(t, secondSameEndpoint, http.StatusBadRequest, 400, "采集配置已存在")

	rtuDevice := serveJSON(router, http.MethodPost, "/api/v1/acquisition/devices", `{"name":"RTU设备","deviceType":"FEED_PROTECTOR","channelId":1,"unitId":1,"pollIntervalMs":1000,"failureThreshold":3,"enabled":1}`, token)
	assertEnvelopeCode(t, rtuDevice, http.StatusOK, 200, "success")
	crossProtocolMove := serveJSON(router, http.MethodPut, "/api/v1/acquisition/devices/3", `{"name":"RTU设备","deviceType":"FEED_PROTECTOR","channelId":2,"unitId":1,"networkEndpoint":{"host":"127.0.0.2","port":1502},"pollIntervalMs":1000,"failureThreshold":3,"enabled":1}`, token)
	assertEnvelopeCode(t, crossProtocolMove, http.StatusBadRequest, 400, "设备不能跨协议移动: MODBUS_RTU -> MODBUS_TCP")
	protocolChange := serveJSON(router, http.MethodPut, "/api/v1/acquisition/channels/1", `{"name":"RTU","protocol":"MODBUS_TCP","timeoutMs":300,"interRequestDelayMs":0,"enabled":1}`, token)
	assertEnvelopeCode(t, protocolChange, http.StatusBadRequest, 400, "通信通道协议不可修改: MODBUS_RTU -> MODBUS_TCP")
}
