package acquisition

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/auth"
	platform "github.com/EziosWJ/edge-collector/edge-collector-api/internal/platform/http"
)

type HandlerService interface {
	PageChannels(context.Context, ChannelQuery) (Page[Channel], error)
	FindChannel(context.Context, int64) (*Channel, error)
	CreateChannel(context.Context, AuditMetadata, ChannelInput) (Channel, error)
	UpdateChannel(context.Context, AuditMetadata, int64, ChannelInput) (Channel, error)
	DeleteChannel(context.Context, AuditMetadata, int64) error
	PageDevices(context.Context, DeviceQuery) (Page[Device], error)
	FindDevice(context.Context, int64) (*Device, error)
	CreateDevice(context.Context, AuditMetadata, DeviceInput) (Device, error)
	UpdateDevice(context.Context, AuditMetadata, int64, DeviceInput) (Device, error)
	DeleteDevice(context.Context, AuditMetadata, int64) error
	BindDeviceScript(context.Context, AuditMetadata, int64, *int64) error
	PageScripts(context.Context, ScriptQuery) (Page[ScriptView], error)
	FindScript(context.Context, int64) (*ScriptView, error)
	CreateScript(context.Context, AuditMetadata, ScriptInput) (ScriptView, error)
	UpdateScript(context.Context, AuditMetadata, int64, ScriptInput) (ScriptView, error)
	DeleteScript(context.Context, AuditMetadata, int64) error
	ValidateScript(context.Context, int64) (ScriptValidationResult, error)
	PublishScript(context.Context, AuditMetadata, int64) (ScriptVersion, error)
	ListScriptVersions(context.Context, int64) ([]ScriptVersion, error)
	RollbackScript(context.Context, AuditMetadata, int64, int64) error
	ListScriptRuntimeStates(context.Context) ([]ScriptRuntimeState, error)
	FindScriptRuntimeState(context.Context, int64) (*ScriptRuntimeState, error)
}

type Handler struct {
	service HandlerService
	states  *CurrentStateStore
}

func NewHandler(service HandlerService, states *CurrentStateStore) (*Handler, error) {
	if service == nil {
		return nil, errors.New("acquisition handler service is required")
	}
	return &Handler{service: service, states: states}, nil
}

func RegisterRoutes(router gin.IRouter, handler *Handler) {
	channels := router.Group("/channels")
	channels.GET("", handler.pageChannels)
	channels.GET("/:id", handler.channelDetail)
	channels.POST("", handler.createChannel)
	channels.PUT("/:id", handler.updateChannel)
	channels.DELETE("/:id", handler.deleteChannel)

	devices := router.Group("/devices")
	devices.GET("", handler.pageDevices)
	devices.GET("/:id", handler.deviceDetail)
	devices.POST("", handler.createDevice)
	devices.PUT("/:id", handler.updateDevice)
	devices.PUT("/:id/script", handler.bindDeviceScript)
	devices.DELETE("/:id/script", handler.unbindDeviceScript)
	devices.DELETE("/:id", handler.deleteDevice)

	scripts := router.Group("/scripts")
	scripts.GET("", handler.pageScripts)
	scripts.GET("/:id", handler.scriptDetail)
	scripts.POST("", handler.createScript)
	scripts.PUT("/:id", handler.updateScript)
	scripts.DELETE("/:id", handler.deleteScript)
	scripts.POST("/:id/validate", handler.validateScript)
	scripts.POST("/:id/publish", handler.publishScript)
	scripts.GET("/:id/versions", handler.listScriptVersions)
	scripts.POST("/:id/rollback", handler.rollbackScript)

	router.GET("/states", handler.statesList)
	router.GET("/states/:id", handler.stateDetail)
	router.GET("/channel-state", handler.channelStatesList)
	router.GET("/script-states", handler.scriptRuntimeStatesList)
	router.GET("/script-states/:id", handler.scriptRuntimeStateDetail)
}

// ApiEnvelope documents the shared HTTP response shape for Swagger.
type ApiEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

type channelRequest struct {
	Name                string               `json:"name"`
	Protocol            string               `json:"protocol"`
	SerialConfig        *serialConfigRequest `json:"serialConfig"`
	TimeoutMS           int                  `json:"timeoutMs"`
	InterRequestDelayMS int                  `json:"interRequestDelayMs"`
	Enabled             int                  `json:"enabled"`
}

type deviceRequest struct {
	ExternalID       string                  `json:"externalId"`
	Name             string                  `json:"name"`
	DeviceType       string                  `json:"deviceType"`
	ChannelID        int64                   `json:"channelId"`
	UnitID           uint8                   `json:"unitId"`
	ScriptID         *int64                  `json:"scriptId"`
	NetworkEndpoint  *networkEndpointRequest `json:"networkEndpoint"`
	PollIntervalMS   int                     `json:"pollIntervalMs"`
	FailureThreshold int                     `json:"failureThreshold"`
	Enabled          int                     `json:"enabled"`
	RegisterBlocks   []registerBlockRequest  `json:"registerBlocks"`
}

type serialConfigRequest struct {
	Port     string `json:"port"`
	BaudRate int    `json:"baudRate"`
	DataBits int    `json:"dataBits"`
	StopBits int    `json:"stopBits"`
	Parity   string `json:"parity"`
}

type networkEndpointRequest struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type registerBlockRequest struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	FunctionCode int    `json:"functionCode"`
	StartAddress int    `json:"startAddress"`
	Quantity     int    `json:"quantity"`
	SortOrder    int    `json:"sortOrder"`
}

type scriptRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	DraftSource string `json:"draftSource"`
}

type scriptRollbackRequest struct {
	VersionID int64 `json:"versionId"`
}

type deviceScriptRequest struct {
	ScriptID *int64 `json:"scriptId"`
}

func (r channelRequest) input() ChannelInput {
	if r.Protocol == "" {
		r.Protocol = ProtocolModbusRTU
	}
	serialConfig := r.SerialConfig
	var serial *SerialConfig
	if serialConfig != nil {
		if serialConfig.BaudRate == 0 {
			serialConfig.BaudRate = 19200
		}
		if serialConfig.DataBits == 0 {
			serialConfig.DataBits = 8
		}
		if serialConfig.StopBits == 0 {
			serialConfig.StopBits = 2
		}
		if strings.TrimSpace(serialConfig.Parity) == "" {
			serialConfig.Parity = "N"
		}
		serial = &SerialConfig{Port: serialConfig.Port, BaudRate: serialConfig.BaudRate, DataBits: serialConfig.DataBits, StopBits: serialConfig.StopBits, Parity: serialConfig.Parity}
	}
	if r.TimeoutMS == 0 {
		r.TimeoutMS = 300
	}
	return ChannelInput{Name: r.Name, Protocol: r.Protocol, SerialConfig: serial, TimeoutMS: r.TimeoutMS, InterRequestDelayMS: r.InterRequestDelayMS, Enabled: r.Enabled}
}

func (r deviceRequest) input() DeviceInput {
	if r.DeviceType == "" {
		r.DeviceType = DeviceTypeFeedProtector
	}
	if r.PollIntervalMS == 0 {
		r.PollIntervalMS = 1000
	}
	if r.FailureThreshold == 0 {
		r.FailureThreshold = 3
	}
	blocks := make([]RegisterBlockInput, len(r.RegisterBlocks))
	for index, block := range r.RegisterBlocks {
		blocks[index] = RegisterBlockInput{ID: block.ID, Name: block.Name, FunctionCode: block.FunctionCode, StartAddress: block.StartAddress, Quantity: block.Quantity, SortOrder: block.SortOrder}
	}
	var endpoint *NetworkEndpoint
	if r.NetworkEndpoint != nil {
		endpoint = &NetworkEndpoint{Host: r.NetworkEndpoint.Host, Port: r.NetworkEndpoint.Port}
	}
	return DeviceInput{ExternalID: r.ExternalID, Name: r.Name, DeviceType: r.DeviceType, ChannelID: r.ChannelID, UnitID: r.UnitID, ScriptID: r.ScriptID, NetworkEndpoint: endpoint, PollIntervalMS: r.PollIntervalMS, FailureThreshold: r.FailureThreshold, Enabled: r.Enabled, RegisterBlocks: blocks}
}

func (r scriptRequest) input() ScriptInput {
	return ScriptInput{Name: r.Name, Description: r.Description, DraftSource: r.DraftSource}
}

// pageChannels godoc
// @Summary Modbus 通道分页
// @Tags 设备采集
// @Security BearerAuth
// @Param page query int false "页码"
// @Param pageSize query int false "每页条数"
// @Param name query string false "通道名称"
// @Param protocol query string false "通信协议"
// @Param enabled query int false "状态：0 或 1"
// @Success 200 {object} ApiEnvelope
// @Router /api/v1/acquisition/channels [get]
func (h *Handler) pageChannels(c *gin.Context) {
	query := ChannelQuery{
		Name: c.Query("name"), Protocol: c.Query("protocol"),
		Page: queryInt(c, "page", 1), PageSize: queryInt(c, "pageSize", 20),
	}
	if raw := c.Query("enabled"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || (value != Enabled && value != Disabled) {
			badRequest(c)
			return
		}
		query.Enabled = &value
	}
	value, err := h.service.PageChannels(c, query)
	h.write(c, value, err)
}

// channelDetail godoc
// @Summary Modbus 通道详情
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "通道 ID"
// @Success 200 {object} ApiEnvelope
// @Router /api/v1/acquisition/channels/{id} [get]
func (h *Handler) channelDetail(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	value, err := h.service.FindChannel(c, id)
	h.write(c, value, err)
}

// createChannel godoc
// @Summary 新建 Modbus 通道
// @Tags 设备采集
// @Security BearerAuth
// @Param request body channelRequest true "通道配置"
// @Success 200 {object} ApiEnvelope
// @Router /api/v1/acquisition/channels [post]
func (h *Handler) createChannel(c *gin.Context) {
	var request channelRequest
	if c.ShouldBindJSON(&request) != nil {
		badRequest(c)
		return
	}
	value, err := h.service.CreateChannel(c, metadata(c), request.input())
	h.write(c, value, err)
}

// updateChannel godoc
// @Summary 修改 Modbus 通道
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "通道 ID"
// @Param request body channelRequest true "通道配置"
// @Success 200 {object} ApiEnvelope
// @Router /api/v1/acquisition/channels/{id} [put]
func (h *Handler) updateChannel(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var request channelRequest
	if c.ShouldBindJSON(&request) != nil {
		badRequest(c)
		return
	}
	value, err := h.service.UpdateChannel(c, metadata(c), id, request.input())
	h.write(c, value, err)
}

// deleteChannel godoc
// @Summary 删除 Modbus 通道
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "通道 ID"
// @Success 200 {object} ApiEnvelope
// @Router /api/v1/acquisition/channels/{id} [delete]
func (h *Handler) deleteChannel(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	h.write(c, nil, h.service.DeleteChannel(c, metadata(c), id))
}

// pageDevices godoc
// @Summary 采集设备分页
// @Tags 设备采集
// @Security BearerAuth
// @Param page query int false "页码"
// @Param pageSize query int false "每页条数"
// @Param channelId query int false "通信通道 ID"
// @Param name query string false "设备名称"
// @Param enabled query int false "状态：0 或 1"
// @Success 200 {object} ApiEnvelope
// @Router /api/v1/acquisition/devices [get]
func (h *Handler) pageDevices(c *gin.Context) {
	query := DeviceQuery{
		Name: c.Query("name"), Page: queryInt(c, "page", 1), PageSize: queryInt(c, "pageSize", 20),
	}
	if raw := c.Query("channelId"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			badRequest(c)
			return
		}
		query.ChannelID = &value
	}
	if raw := c.Query("enabled"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || (value != Enabled && value != Disabled) {
			badRequest(c)
			return
		}
		query.Enabled = &value
	}
	value, err := h.service.PageDevices(c, query)
	h.write(c, value, err)
}

// deviceDetail godoc
// @Summary 采集设备详情
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "设备 ID"
// @Success 200 {object} ApiEnvelope
// @Router /api/v1/acquisition/devices/{id} [get]
func (h *Handler) deviceDetail(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	value, err := h.service.FindDevice(c, id)
	h.write(c, value, err)
}

// createDevice godoc
// @Summary 新建采集设备
// @Tags 设备采集
// @Security BearerAuth
// @Param request body deviceRequest true "设备配置"
// @Success 200 {object} ApiEnvelope
// @Router /api/v1/acquisition/devices [post]
func (h *Handler) createDevice(c *gin.Context) {
	var request deviceRequest
	if c.ShouldBindJSON(&request) != nil {
		badRequest(c)
		return
	}
	value, err := h.service.CreateDevice(c, metadata(c), request.input())
	h.write(c, value, err)
}

// updateDevice godoc
// @Summary 修改采集设备
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "设备 ID"
// @Param request body deviceRequest true "设备配置"
// @Success 200 {object} ApiEnvelope
// @Router /api/v1/acquisition/devices/{id} [put]
func (h *Handler) updateDevice(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var request deviceRequest
	if c.ShouldBindJSON(&request) != nil {
		badRequest(c)
		return
	}
	value, err := h.service.UpdateDevice(c, metadata(c), id, request.input())
	h.write(c, value, err)
}

// deleteDevice godoc
// @Summary 删除采集设备
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "设备 ID"
// @Success 200 {object} ApiEnvelope
// @Router /api/v1/acquisition/devices/{id} [delete]
func (h *Handler) deleteDevice(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	h.write(c, nil, h.service.DeleteDevice(c, metadata(c), id))
}

// bindDeviceScript godoc
// @Summary 绑定设备协议脚本
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "设备 ID"
// @Param request body deviceScriptRequest true "脚本绑定；scriptId 必须指向已发布脚本"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/acquisition/devices/{id}/script [put]
func (h *Handler) bindDeviceScript(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var request deviceScriptRequest
	if c.ShouldBindJSON(&request) != nil {
		badRequest(c)
		return
	}
	h.write(c, nil, h.service.BindDeviceScript(c, metadata(c), id, request.ScriptID))
}

// unbindDeviceScript godoc
// @Summary 解绑设备协议脚本
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "设备 ID"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/acquisition/devices/{id}/script [delete]
func (h *Handler) unbindDeviceScript(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	h.write(c, nil, h.service.BindDeviceScript(c, metadata(c), id, nil))
}

// pageScripts godoc
// @Summary 协议脚本分页
// @Tags 设备采集
// @Security BearerAuth
// @Param page query int false "页码"
// @Param pageSize query int false "每页条数"
// @Param name query string false "脚本名称"
// @Param published query int false "发布状态：0 或 1"
// @Param bound query int false "绑定设备状态：0 或 1"
// @Success 200 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/acquisition/scripts [get]
func (h *Handler) pageScripts(c *gin.Context) {
	query := ScriptQuery{
		Name: c.Query("name"), Page: queryInt(c, "page", 1), PageSize: queryInt(c, "pageSize", 20),
	}
	if published, ok := queryOptionalFlag(c, "published"); !ok {
		badRequest(c)
		return
	} else {
		query.Published = published
	}
	if bound, ok := queryOptionalFlag(c, "bound"); !ok {
		badRequest(c)
		return
	} else {
		query.Bound = bound
	}
	value, err := h.service.PageScripts(c, query)
	h.write(c, value, err)
}

// scriptDetail godoc
// @Summary 协议脚本详情
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "脚本 ID"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 404 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/acquisition/scripts/{id} [get]
func (h *Handler) scriptDetail(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	value, err := h.service.FindScript(c, id)
	h.write(c, value, err)
}

// createScript godoc
// @Summary 新建协议脚本草稿
// @Tags 设备采集
// @Security BearerAuth
// @Param request body scriptRequest true "协议脚本草稿"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/acquisition/scripts [post]
func (h *Handler) createScript(c *gin.Context) {
	var request scriptRequest
	if c.ShouldBindJSON(&request) != nil {
		badRequest(c)
		return
	}
	value, err := h.service.CreateScript(c, metadata(c), request.input())
	h.write(c, value, err)
}

// updateScript godoc
// @Summary 修改协议脚本草稿
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "脚本 ID"
// @Param request body scriptRequest true "协议脚本草稿"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 404 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/acquisition/scripts/{id} [put]
func (h *Handler) updateScript(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var request scriptRequest
	if c.ShouldBindJSON(&request) != nil {
		badRequest(c)
		return
	}
	value, err := h.service.UpdateScript(c, metadata(c), id, request.input())
	h.write(c, value, err)
}

// deleteScript godoc
// @Summary 删除协议脚本
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "脚本 ID"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 404 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/acquisition/scripts/{id} [delete]
func (h *Handler) deleteScript(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	h.write(c, nil, h.service.DeleteScript(c, metadata(c), id))
}

// validateScript godoc
// @Summary 校验协议脚本草稿
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "脚本 ID"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 404 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/acquisition/scripts/{id}/validate [post]
func (h *Handler) validateScript(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	value, err := h.service.ValidateScript(c, id)
	h.write(c, value, err)
}

// publishScript godoc
// @Summary 发布协议脚本
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "脚本 ID"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 404 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/acquisition/scripts/{id}/publish [post]
func (h *Handler) publishScript(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	value, err := h.service.PublishScript(c, metadata(c), id)
	h.write(c, value, err)
}

// listScriptVersions godoc
// @Summary 协议脚本版本历史
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "脚本 ID"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 404 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/acquisition/scripts/{id}/versions [get]
func (h *Handler) listScriptVersions(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	value, err := h.service.ListScriptVersions(c, id)
	h.write(c, value, err)
}

// rollbackScript godoc
// @Summary 回滚协议脚本版本
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "脚本 ID"
// @Param request body scriptRollbackRequest true "历史版本 ID"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 404 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/acquisition/scripts/{id}/rollback [post]
func (h *Handler) rollbackScript(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var request scriptRollbackRequest
	if c.ShouldBindJSON(&request) != nil {
		badRequest(c)
		return
	}
	h.write(c, nil, h.service.RollbackScript(c, metadata(c), id, request.VersionID))
}

// statesList godoc
// @Summary 设备当前状态列表
// @Tags 设备采集
// @Security BearerAuth
// @Success 200 {object} ApiEnvelope
// @Router /api/v1/acquisition/states [get]
func (h *Handler) statesList(c *gin.Context) {
	if h.states == nil {
		platform.OK(c, []CurrentState{})
		return
	}
	platform.OK(c, h.states.List())
}

// stateDetail godoc
// @Summary 设备当前状态详情
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "设备 ID"
// @Success 200 {object} ApiEnvelope
// @Router /api/v1/acquisition/states/{id} [get]
func (h *Handler) stateDetail(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if h.states != nil {
		if value, found := h.states.Get(id); found {
			platform.OK(c, value)
			return
		}
	}
	platform.WriteError(c, http.StatusNotFound, platform.CodeNotFound, ErrNotFound.Error(), nil)
}

// channelStatesList godoc
// @Summary 通信通道运行状态列表
// @Tags 设备采集
// @Security BearerAuth
// @Success 200 {object} ApiEnvelope
// @Router /api/v1/acquisition/channel-state [get]
func (h *Handler) channelStatesList(c *gin.Context) {
	if h.states == nil {
		platform.OK(c, []ChannelRuntimeState{})
		return
	}
	platform.OK(c, h.states.ChannelStates())
}

// scriptRuntimeStatesList godoc
// @Summary 动态脚本运行观察列表
// @Tags 设备采集
// @Security BearerAuth
// @Success 200 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/acquisition/script-states [get]
func (h *Handler) scriptRuntimeStatesList(c *gin.Context) {
	value, err := h.service.ListScriptRuntimeStates(c)
	h.write(c, value, err)
}

// scriptRuntimeStateDetail godoc
// @Summary 动态脚本运行观察详情
// @Tags 设备采集
// @Security BearerAuth
// @Param id path int true "设备 ID"
// @Success 200 {object} ApiEnvelope
// @Failure 400 {object} ApiEnvelope
// @Failure 404 {object} ApiEnvelope
// @Failure 401 {object} ApiEnvelope
// @Router /api/v1/acquisition/script-states/{id} [get]
func (h *Handler) scriptRuntimeStateDetail(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	value, err := h.service.FindScriptRuntimeState(c, id)
	h.write(c, value, err)
}

func (h *Handler) write(c *gin.Context, value any, err error) {
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			platform.WriteError(c, http.StatusNotFound, platform.CodeNotFound, err.Error(), nil)
		case errors.Is(err, ErrScriptValidatorUnavailable):
			platform.TemporaryUnavailable(c)
		case isScriptValidationFailure(err):
			var validationErr *ScriptValidationFailedError
			if errors.As(err, &validationErr) {
				platform.WriteError(c, http.StatusBadRequest, platform.CodeBadRequest, validationErr.Error(), validationErr.Result)
				return
			}
		case errors.Is(err, ErrInvalid), errors.Is(err, ErrConflict), errors.Is(err, ErrChannelHasDevices), errors.Is(err, ErrUnsupportedDevice), errors.Is(err, ErrProtocolImmutable), errors.Is(err, ErrCrossProtocolMove):
			platform.WriteError(c, http.StatusBadRequest, platform.CodeBadRequest, err.Error(), nil)
		case platform.IsTemporaryUnavailable(err):
			platform.TemporaryUnavailable(c)
		default:
			platform.WriteError(c, http.StatusInternalServerError, platform.CodeInternalError, "系统错误", nil)
		}
		return
	}
	platform.OK(c, value)
}

func isScriptValidationFailure(err error) bool {
	var validationErr *ScriptValidationFailedError
	return errors.As(err, &validationErr)
}

func metadata(c *gin.Context) AuditMetadata {
	principal, _ := auth.PrincipalFromContext(c.Request.Context())
	requestMeta, _ := platform.RequestMetaFromContext(c.Request.Context())
	return AuditMetadata{ActorID: principal.UserID, RequestID: requestMeta.RequestID, ClientIP: requestMeta.ClientIP, UserAgent: requestMeta.UserAgent, RequestMethod: c.Request.Method, RequestURL: c.Request.URL.RequestURI()}
}

func queryInt(c *gin.Context, key string, fallback int) int {
	value, err := strconv.Atoi(c.Query(key))
	if c.Query(key) == "" || err != nil {
		return fallback
	}
	return value
}

func queryOptionalFlag(c *gin.Context, key string) (*bool, bool) {
	raw := c.Query(key)
	if raw == "" {
		return nil, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || (value != 0 && value != 1) {
		return nil, false
	}
	flag := value == 1
	return &flag, true
}

func pathID(c *gin.Context) (int64, bool) {
	value, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || value < 1 {
		badRequest(c)
		return 0, false
	}
	return value, true
}

func badRequest(c *gin.Context) {
	platform.WriteError(c, http.StatusBadRequest, platform.CodeBadRequest, "参数错误", nil)
}
