package mqtt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type mqttHandlerServiceFake struct {
	config       ConfigView
	state        RuntimeStateView
	stats        OutboxStatsView
	commands     Page[CommandJournalView]
	commandQuery CommandJournalQuery
	command      *CommandJournalView
	updated      ConfigInput
	metadata     audit.Metadata
	testInput    *ConfigInput
	testResponse TestConnectionView

	updateErr   error
	testErr     error
	stateErr    error
	statsErr    error
	commandsErr error
	commandErr  error
}

func (f *mqttHandlerServiceFake) GetConfig(context.Context) (ConfigView, error) {
	return f.config, nil
}

func (f *mqttHandlerServiceFake) UpdateConfig(_ context.Context, metadata audit.Metadata, input ConfigInput) (ConfigView, error) {
	f.metadata = metadata
	f.updated = input
	return f.config, f.updateErr
}

func (f *mqttHandlerServiceFake) TestConnection(_ context.Context, input *ConfigInput) (TestConnectionView, error) {
	f.testInput = input
	return f.testResponse, f.testErr
}

func (f *mqttHandlerServiceFake) State(context.Context) (RuntimeStateView, error) {
	return f.state, f.stateErr
}

func (f *mqttHandlerServiceFake) OutboxStats(context.Context) (OutboxStatsView, error) {
	return f.stats, f.statsErr
}

func (f *mqttHandlerServiceFake) PageCommands(_ context.Context, query CommandJournalQuery) (Page[CommandJournalView], error) {
	f.commandQuery = query
	return f.commands, f.commandsErr
}

func (f *mqttHandlerServiceFake) FindCommand(context.Context, string) (*CommandJournalView, error) {
	return f.command, f.commandErr
}

func newMQTTHandlerTestRouter(service HandlerService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1/mqtt"), mustNewMQTTHandler(service))
	return router
}

func mustNewMQTTHandler(service HandlerService) *Handler {
	handler, err := NewHandler(service)
	if err != nil {
		panic(err)
	}
	return handler
}

func TestMQTTHandlerConfigNeverReturnsSecretMaterial(t *testing.T) {
	service := &mqttHandlerServiceFake{config: ConfigView{
		Enabled: true, EdgeID: "edge-01", BrokerURL: "mqtt://broker:1883", ProtocolVersion: ProtocolMQTT5,
		PasswordConfigured: true, ClientPrivateKeyConfigured: true,
	}}
	response := serveMQTTHandlerRequest(newMQTTHandlerTestRouter(service), http.MethodGet, "/api/v1/mqtt/config", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET config status = %d, body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{"passwordCiphertext", "clientPrivateKeyCiphertext", "ciphertext", "secret-value"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("GET config response contains forbidden material %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"passwordConfigured":true`) || !strings.Contains(body, `"clientPrivateKeyConfigured":true`) {
		t.Fatalf("GET config response did not preserve configured flags: %s", body)
	}
}

func TestMQTTHandlerConfigMapsExplicitSecretActions(t *testing.T) {
	service := &mqttHandlerServiceFake{config: ConfigView{EdgeID: "edge-01"}}
	request := `{"enabled":true,"edgeId":"edge-01","brokerUrl":"mqtt://broker:1883","protocolVersion":"MQTT_5","clientId":"edge-collector","username":"operator","passwordAction":"set","password":"new-password","tlsEnabled":true,"caCertificate":"CA PEM","clientCertificate":"CERT PEM","clientPrivateKeyAction":"clear","keepAliveSeconds":30,"connectTimeoutMs":1000,"reconnectMinMs":100,"reconnectMaxMs":1000,"topicPrefix":"edge","rawPublishIntervalMs":1000,"outboxMaxRows":10000,"outboxMaxBytes":67108864,"outboxRetentionDays":7,"commandJournalRetentionDays":7,"commandJournalMaxRows":10000,"commandQueueCapacity":32,"commandPollFairness":1}`
	response := serveMQTTHandlerRequest(newMQTTHandlerTestRouter(service), http.MethodPut, "/api/v1/mqtt/config", "application/json", strings.NewReader(request))
	if response.Code != http.StatusOK {
		t.Fatalf("PUT config status = %d, body=%s", response.Code, response.Body.String())
	}
	if service.updated.PasswordAction != SecretSet || service.updated.Password != "new-password" {
		t.Fatalf("password action/value = %q/%q", service.updated.PasswordAction, service.updated.Password)
	}
	if service.updated.ClientPrivateKeyAction != SecretClear || service.updated.ClientPrivateKey != "" {
		t.Fatalf("private key action/value = %q/%q", service.updated.ClientPrivateKeyAction, service.updated.ClientPrivateKey)
	}
}

func TestMQTTHandlerKeepDoesNotAcceptMaskedSecret(t *testing.T) {
	service := &mqttHandlerServiceFake{updateErr: ErrInvalidConfig}
	request := `{"enabled":false,"edgeId":"edge-01","brokerUrl":"mqtt://broker:1883","protocolVersion":"MQTT_5","clientId":"edge-collector","passwordAction":"keep","password":"********"}`
	response := serveMQTTHandlerRequest(newMQTTHandlerTestRouter(service), http.MethodPut, "/api/v1/mqtt/config", "application/json", strings.NewReader(request))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("masked keep status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestMQTTHandlerTestConnectionUsesOptionalCurrentConfig(t *testing.T) {
	service := &mqttHandlerServiceFake{testResponse: TestConnectionView{Success: true, RuntimeConnected: true}}
	response := serveMQTTHandlerRequest(newMQTTHandlerTestRouter(service), http.MethodPost, "/api/v1/mqtt/test-connection", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("test connection status = %d, body=%s", response.Code, response.Body.String())
	}
	if service.testInput != nil {
		t.Fatalf("empty test-connection body should use current config, got %#v", service.testInput)
	}
	if !strings.Contains(response.Body.String(), `"success":true`) || !strings.Contains(response.Body.String(), `"runtimeConnected":true`) {
		t.Fatalf("test connection response = %s", response.Body.String())
	}
}

func TestMQTTHandlerTestConnectionMapsSubmittedConfig(t *testing.T) {
	service := &mqttHandlerServiceFake{testResponse: TestConnectionView{Success: true}}
	request := `{"enabled":true,"edgeId":"edge-01","brokerUrl":"mqtt://broker:1883","protocolVersion":"MQTT_5","clientId":"edge-collector","passwordAction":"keep","clientPrivateKeyAction":"keep","keepAliveSeconds":30,"connectTimeoutMs":1000,"reconnectMinMs":100,"reconnectMaxMs":1000,"topicPrefix":"edge","rawPublishIntervalMs":1000,"outboxMaxRows":10000,"outboxMaxBytes":67108864,"outboxRetentionDays":7,"commandJournalRetentionDays":7,"commandJournalMaxRows":10000,"commandQueueCapacity":32,"commandPollFairness":1}`
	response := serveMQTTHandlerRequest(newMQTTHandlerTestRouter(service), http.MethodPost, "/api/v1/mqtt/test-connection", "application/json", strings.NewReader(request))
	if response.Code != http.StatusOK {
		t.Fatalf("test connection status = %d, body=%s", response.Code, response.Body.String())
	}
	if service.testInput == nil || service.testInput.BrokerURL != "mqtt://broker:1883" {
		t.Fatalf("submitted test config = %#v", service.testInput)
	}
}

func TestMQTTHandlerExposesStateStatsAndCommandQueries(t *testing.T) {
	now := time.Date(2026, 9, 17, 5, 0, 0, 0, time.UTC)
	service := &mqttHandlerServiceFake{
		state:    RuntimeStateView{RuntimeSnapshot: RuntimeSnapshot{State: RuntimeStateConnected, Connected: true, SubscriptionFilter: "edge/edge-01/device/+/command"}, PendingLatestCount: 2},
		stats:    OutboxStatsView{Rows: 3, Bytes: 42, OldestAge: 9, MaxRows: 10, MaxBytes: 100, RowUtilization: 0.3, ByteUtilization: 0.42},
		commands: Page[CommandJournalView]{Records: []CommandJournalView{{CommandID: "cmd-1", DeviceID: "device-1", CommandName: "close", Status: CommandStatusSucceeded, ReceivedAt: now}}, Total: 1, Page: 2, PageSize: 5},
		command:  &CommandJournalView{CommandID: "cmd-1", DeviceID: "device-1", CommandName: "close", Status: CommandStatusSucceeded, ReceivedAt: now},
	}
	router := newMQTTHandlerTestRouter(service)
	for _, path := range []string{"/api/v1/mqtt/state", "/api/v1/mqtt/outbox/stats", "/api/v1/mqtt/commands?page=2&pageSize=5&status=SUCCEEDED&deviceId=device-1&commandId=cmd-&name=los", "/api/v1/mqtt/commands/cmd-1"} {
		response := serveMQTTHandlerRequest(router, http.MethodGet, path, "", nil)
		if response.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, body=%s", path, response.Code, response.Body.String())
		}
	}
	if service.commandQuery != (CommandJournalQuery{Page: 2, PageSize: 5, Status: CommandStatusSucceeded, DeviceID: "device-1", CommandID: "cmd-", Name: "los"}) {
		t.Fatalf("command query = %#v", service.commandQuery)
	}
	if !strings.Contains(serveMQTTHandlerRequest(router, http.MethodGet, "/api/v1/mqtt/state", "", nil).Body.String(), `"pendingLatestCount":2`) {
		t.Fatal("state response did not include pending latest count")
	}
}

func TestMQTTHandlerRejectsNonPositiveCommandPageParameters(t *testing.T) {
	router := newMQTTHandlerTestRouter(&mqttHandlerServiceFake{})
	for _, target := range []string{"/api/v1/mqtt/commands?page=0", "/api/v1/mqtt/commands?pageSize=0"} {
		response := serveMQTTHandlerRequest(router, http.MethodGet, target, "", nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, body=%s", target, response.Code, response.Body.String())
		}
	}
}

func TestMQTTHandlerMapsNotFoundAndSafeTestFailure(t *testing.T) {
	service := &mqttHandlerServiceFake{commandErr: gorm.ErrRecordNotFound, testErr: fmt.Errorf("%w: broker password must not appear", ErrTestConnectionFailed)}
	router := newMQTTHandlerTestRouter(service)
	response := serveMQTTHandlerRequest(router, http.MethodGet, "/api/v1/mqtt/commands/missing", "", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing command status = %d, body=%s", response.Code, response.Body.String())
	}
	response = serveMQTTHandlerRequest(router, http.MethodPost, "/api/v1/mqtt/test-connection", "", nil)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "broker password") {
		t.Fatalf("test failure response leaked detail or wrong status = %d body=%s", response.Code, response.Body.String())
	}
}

func serveMQTTHandlerRequest(router http.Handler, method, target, contentType string, body io.Reader) *httptest.ResponseRecorder {
	if body == nil {
		body = strings.NewReader("")
	}
	request := httptest.NewRequest(method, target, body)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
