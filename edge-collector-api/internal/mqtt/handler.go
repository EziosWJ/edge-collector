package mqtt

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/auth"
	platformhttp "github.com/EziosWJ/edge-collector/edge-collector-api/internal/platform/http"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Handler serves the authenticated MQTT management API.
type Handler struct {
	service HandlerService
}

func NewHandler(service HandlerService) (*Handler, error) {
	if service == nil {
		return nil, errors.New("MQTT handler service is required")
	}
	return &Handler{service: service}, nil
}

func RegisterRoutes(router gin.IRouter, handler *Handler) {
	router.GET("/config", handler.getConfig)
	router.PUT("/config", handler.updateConfig)
	router.POST("/test-connection", handler.testConnection)
	router.GET("/state", handler.state)
	router.GET("/outbox/stats", handler.outboxStats)
	router.GET("/commands", handler.pageCommands)
	router.GET("/commands/:commandId", handler.commandDetail)
}

// ApiEnvelope is the shared response shape used by the generated Swagger
// document for the MQTT management endpoints.
type ApiEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

type configRequest struct {
	Enabled                     bool         `json:"enabled"`
	EdgeID                      string       `json:"edgeId"`
	BrokerURL                   string       `json:"brokerUrl"`
	ProtocolVersion             string       `json:"protocolVersion"`
	ClientID                    string       `json:"clientId"`
	Username                    string       `json:"username"`
	PasswordAction              SecretAction `json:"passwordAction"`
	Password                    string       `json:"password,omitempty"`
	TLSEnabled                  bool         `json:"tlsEnabled"`
	CACertificate               string       `json:"caCertificate"`
	ClientCertificate           string       `json:"clientCertificate"`
	ClientPrivateKeyAction      SecretAction `json:"clientPrivateKeyAction"`
	ClientPrivateKey            string       `json:"clientPrivateKey,omitempty"`
	KeepAliveSeconds            int          `json:"keepAliveSeconds"`
	ConnectTimeoutMS            int          `json:"connectTimeoutMs"`
	ReconnectMinMS              int          `json:"reconnectMinMs"`
	ReconnectMaxMS              int          `json:"reconnectMaxMs"`
	TopicPrefix                 string       `json:"topicPrefix"`
	RawPublishIntervalMS        int          `json:"rawPublishIntervalMs"`
	OutboxMaxRows               int          `json:"outboxMaxRows"`
	OutboxMaxBytes              int64        `json:"outboxMaxBytes"`
	OutboxRetentionDays         int          `json:"outboxRetentionDays"`
	CommandJournalRetentionDays int          `json:"commandJournalRetentionDays"`
	CommandJournalMaxRows       int          `json:"commandJournalMaxRows"`
	CommandQueueCapacity        int          `json:"commandQueueCapacity"`
	CommandPollFairness         int          `json:"commandPollFairness"`
}

func (r configRequest) input() ConfigInput {
	passwordAction := r.PasswordAction
	if passwordAction == "" {
		passwordAction = SecretKeep
	}
	privateKeyAction := r.ClientPrivateKeyAction
	if privateKeyAction == "" {
		privateKeyAction = SecretKeep
	}
	return ConfigInput{
		Enabled:                     r.Enabled,
		EdgeID:                      r.EdgeID,
		BrokerURL:                   r.BrokerURL,
		ProtocolVersion:             r.ProtocolVersion,
		ClientID:                    r.ClientID,
		Username:                    r.Username,
		PasswordAction:              passwordAction,
		Password:                    r.Password,
		TLSEnabled:                  r.TLSEnabled,
		CACertificate:               r.CACertificate,
		ClientCertificate:           r.ClientCertificate,
		ClientPrivateKeyAction:      privateKeyAction,
		ClientPrivateKey:            r.ClientPrivateKey,
		KeepAliveSeconds:            r.KeepAliveSeconds,
		ConnectTimeoutMS:            r.ConnectTimeoutMS,
		ReconnectMinMS:              r.ReconnectMinMS,
		ReconnectMaxMS:              r.ReconnectMaxMS,
		TopicPrefix:                 r.TopicPrefix,
		RawPublishIntervalMS:        r.RawPublishIntervalMS,
		OutboxMaxRows:               r.OutboxMaxRows,
		OutboxMaxBytes:              r.OutboxMaxBytes,
		OutboxRetentionDays:         r.OutboxRetentionDays,
		CommandJournalRetentionDays: r.CommandJournalRetentionDays,
		CommandJournalMaxRows:       r.CommandJournalMaxRows,
		CommandQueueCapacity:        r.CommandQueueCapacity,
		CommandPollFairness:         r.CommandPollFairness,
	}
}

// getConfig godoc
// @Summary 获取 MQTT 配置
// @Tags MQTT 管理
// @Security BearerAuth
// @Success 200 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Failure 404 {object} ApiEnvelope
// @Router /api/v1/mqtt/config [get]
func (h *Handler) getConfig(c *gin.Context) {
	value, err := h.service.GetConfig(c.Request.Context())
	h.write(c, value, err)
}

// updateConfig godoc
// @Summary 更新 MQTT 配置
// @Tags MQTT 管理
// @Security BearerAuth
// @Param request body configRequest true "MQTT 配置；secret 使用 keep/set/clear"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/mqtt/config [put]
func (h *Handler) updateConfig(c *gin.Context) {
	var request configRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		platformhttp.WriteError(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "参数错误", nil)
		return
	}
	value, err := h.service.UpdateConfig(c.Request.Context(), mqttAuditMetadata(c), request.input())
	h.write(c, value, err)
}

// testConnection godoc
// @Summary 测试 MQTT 连接
// @Description 可选提交完整配置；不提交 body 时测试当前已保存配置。该操作不改变正式 MQTT runtime。
// @Tags MQTT 管理
// @Security BearerAuth
// @Param request body configRequest false "可选的待测试 MQTT 配置"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/mqtt/test-connection [post]
func (h *Handler) testConnection(c *gin.Context) {
	var request configRequest
	err := c.ShouldBindJSON(&request)
	var input *ConfigInput
	if err == nil {
		value := request.input()
		input = &value
	} else if !errors.Is(err, io.EOF) {
		platformhttp.WriteError(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "参数错误", nil)
		return
	}
	value, err := h.service.TestConnection(c.Request.Context(), input)
	h.write(c, value, err)
}

// state godoc
// @Summary 获取 MQTT runtime 状态
// @Tags MQTT 管理
// @Security BearerAuth
// @Success 200 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/mqtt/state [get]
func (h *Handler) state(c *gin.Context) {
	value, err := h.service.State(c.Request.Context())
	h.write(c, value, err)
}

// outboxStats godoc
// @Summary 获取 MQTT outbox 健康统计
// @Tags MQTT 管理
// @Security BearerAuth
// @Success 200 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/mqtt/outbox/stats [get]
func (h *Handler) outboxStats(c *gin.Context) {
	value, err := h.service.OutboxStats(c.Request.Context())
	h.write(c, value, err)
}

// pageCommands godoc
// @Summary 分页查询 MQTT command journal
// @Tags MQTT 管理
// @Security BearerAuth
// @Param page query int false "页码"
// @Param pageSize query int false "每页条数"
// @Param status query string false "命令状态"
// @Param deviceId query string false "设备外部 ID"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/mqtt/commands [get]
func (h *Handler) pageCommands(c *gin.Context) {
	page, ok := mqttQueryInt(c, "page", 1)
	if !ok {
		mqttBadRequest(c)
		return
	}
	pageSize, ok := mqttQueryInt(c, "pageSize", 20)
	if !ok {
		mqttBadRequest(c)
		return
	}
	value, err := h.service.PageCommands(c.Request.Context(), CommandJournalQuery{
		Page: page, PageSize: pageSize, Status: c.Query("status"), DeviceID: c.Query("deviceId"),
	})
	h.write(c, value, err)
}

// commandDetail godoc
// @Summary 查看 MQTT command journal 详情
// @Tags MQTT 管理
// @Security BearerAuth
// @Param commandId path string true "commandId"
// @Success 200 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Failure 404 {object} ApiEnvelope
// @Router /api/v1/mqtt/commands/{commandId} [get]
func (h *Handler) commandDetail(c *gin.Context) {
	commandID := strings.TrimSpace(c.Param("commandId"))
	if commandID == "" {
		mqttBadRequest(c)
		return
	}
	value, err := h.service.FindCommand(c.Request.Context(), commandID)
	h.write(c, value, err)
}

func (h *Handler) write(c *gin.Context, value any, err error) {
	if err != nil {
		writeMQTTError(c, err)
		return
	}
	platformhttp.OK(c, value)
}

func writeMQTTError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, ErrCommandNotFound):
		platformhttp.WriteError(c, http.StatusNotFound, platformhttp.CodeNotFound, "数据不存在", nil)
	case errors.Is(err, ErrMasterSecretRequired):
		platformhttp.WriteError(c, http.StatusBadRequest, platformhttp.CodeBadRequest, ErrMasterSecretRequired.Error(), nil)
	case errors.Is(err, ErrInvalidConfig):
		platformhttp.WriteError(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error(), nil)
	case errors.Is(err, ErrTestConnectionFailed):
		// Connector errors can contain broker/library diagnostics. Keep the
		// management response stable and never reflect credentials.
		platformhttp.WriteError(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "MQTT test connection failed", nil)
	case errors.Is(err, ErrReliableResultCapacityExhausted), errors.Is(err, ErrCommandJournalCapacity):
		platformhttp.WriteError(c, http.StatusBadRequest, platformhttp.CodeBadRequest, err.Error(), nil)
	case platformhttp.IsTemporaryUnavailable(err):
		platformhttp.TemporaryUnavailable(c)
	default:
		platformhttp.WriteError(c, http.StatusInternalServerError, platformhttp.CodeInternalError, "系统错误", nil)
	}
}

func mqttBadRequest(c *gin.Context) {
	platformhttp.WriteError(c, http.StatusBadRequest, platformhttp.CodeBadRequest, "参数错误", nil)
}

func mqttQueryInt(c *gin.Context, key string, fallback int) (int, bool) {
	value := c.Query(key)
	if value == "" {
		return fallback, true
	}
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil && parsed > 0
}

func mqttAuditMetadata(c *gin.Context) audit.Metadata {
	principal, _ := auth.PrincipalFromContext(c.Request.Context())
	requestMeta, _ := platformhttp.RequestMetaFromContext(c.Request.Context())
	return audit.Metadata{
		ActorID: principal.UserID, RequestID: requestMeta.RequestID, ClientIP: requestMeta.ClientIP,
		UserAgent: requestMeta.UserAgent, RequestMethod: c.Request.Method, RequestURL: c.Request.URL.RequestURI(),
	}
}
