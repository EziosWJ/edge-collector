package acquisition

import "time"

const (
	DeviceTypeFeedProtector = "FEED_PROTECTOR"
	Enabled                 = 1
	Disabled                = 0

	ProtocolModbusRTU        = "MODBUS_RTU"
	ProtocolModbusTCP        = "MODBUS_TCP"
	ProtocolModbusUDP        = "MODBUS_UDP"
	ProtocolModbusRTUOverUDP = "MODBUS_RTU_OVER_UDP"
)

type SerialConfig struct {
	Port     string `json:"port"`
	BaudRate int    `json:"baudRate"`
	DataBits int    `json:"dataBits"`
	StopBits int    `json:"stopBits"`
	Parity   string `json:"parity"`
}

type NetworkEndpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type Channel struct {
	ID                  int64         `gorm:"column:id;primaryKey" json:"id"`
	Name                string        `gorm:"column:name" json:"name"`
	Protocol            string        `gorm:"column:protocol" json:"protocol"`
	SerialConfig        *SerialConfig `gorm:"-" json:"serialConfig,omitempty"`
	TimeoutMS           int           `gorm:"column:timeout_ms" json:"timeoutMs"`
	InterRequestDelayMS int           `gorm:"column:inter_request_delay_ms" json:"interRequestDelayMs"`
	Enabled             int           `gorm:"column:enabled" json:"enabled"`
	CreateTime          time.Time     `gorm:"column:create_time;autoCreateTime" json:"createTime"`
	UpdateTime          time.Time     `gorm:"column:update_time;autoUpdateTime" json:"updateTime"`
	Deleted             int           `gorm:"column:deleted" json:"-"`
}

func (Channel) TableName() string { return "acquisition_channel" }

type SerialChannel struct {
	ChannelID  int64     `gorm:"column:channel_id;primaryKey" json:"channelId"`
	Port       string    `gorm:"column:port" json:"port"`
	BaudRate   int       `gorm:"column:baud_rate" json:"baudRate"`
	DataBits   int       `gorm:"column:data_bits" json:"dataBits"`
	StopBits   int       `gorm:"column:stop_bits" json:"stopBits"`
	Parity     string    `gorm:"column:parity" json:"parity"`
	CreateTime time.Time `gorm:"column:create_time;autoCreateTime" json:"createTime"`
	UpdateTime time.Time `gorm:"column:update_time;autoUpdateTime" json:"updateTime"`
}

func (SerialChannel) TableName() string { return "acquisition_serial_channel" }

type Device struct {
	ScriptID         *int64           `gorm:"column:script_id" json:"scriptId,omitempty"`
	ID               int64            `gorm:"column:id;primaryKey" json:"id"`
	Name             string           `gorm:"column:name" json:"name"`
	DeviceType       string           `gorm:"column:device_type" json:"deviceType"`
	ChannelID        int64            `gorm:"column:channel_id" json:"channelId"`
	UnitID           uint8            `gorm:"column:unit_id" json:"unitId"`
	NetworkEndpoint  *NetworkEndpoint `gorm:"-" json:"networkEndpoint,omitempty"`
	PollIntervalMS   int              `gorm:"column:poll_interval_ms" json:"pollIntervalMs"`
	FailureThreshold int              `gorm:"column:failure_threshold" json:"failureThreshold"`
	Enabled          int              `gorm:"column:enabled" json:"enabled"`
	CreateTime       time.Time        `gorm:"column:create_time;autoCreateTime" json:"createTime"`
	UpdateTime       time.Time        `gorm:"column:update_time;autoUpdateTime" json:"updateTime"`
	Deleted          int              `gorm:"column:deleted" json:"-"`
	RegisterBlocks   []RegisterBlock  `gorm:"-" json:"registerBlocks"`
}

func (Device) TableName() string { return "acquisition_device" }

type NetworkDevice struct {
	DeviceID   int64     `gorm:"column:device_id;primaryKey" json:"deviceId"`
	Host       string    `gorm:"column:host" json:"host"`
	Port       int       `gorm:"column:port" json:"port"`
	CreateTime time.Time `gorm:"column:create_time;autoCreateTime" json:"createTime"`
	UpdateTime time.Time `gorm:"column:update_time;autoUpdateTime" json:"updateTime"`
}

func (NetworkDevice) TableName() string { return "acquisition_network_device" }

type ChannelInput struct {
	Name                string
	Protocol            string
	SerialConfig        *SerialConfig
	TimeoutMS           int
	InterRequestDelayMS int
	Enabled             int
}

type DeviceInput struct {
	Name             string
	DeviceType       string
	ChannelID        int64
	UnitID           uint8
	ScriptID         *int64
	NetworkEndpoint  *NetworkEndpoint
	PollIntervalMS   int
	FailureThreshold int
	Enabled          int
	RegisterBlocks   []RegisterBlockInput
}

const (
	FunctionCodeReadHoldingRegisters = 3
	FunctionCodeReadInputRegisters   = 4
)

type RegisterBlock struct {
	ID           int64     `gorm:"column:id;primaryKey" json:"id"`
	DeviceID     int64     `gorm:"column:device_id" json:"deviceId"`
	Name         string    `gorm:"column:name" json:"name"`
	FunctionCode int       `gorm:"column:function_code" json:"functionCode"`
	StartAddress int       `gorm:"column:start_address" json:"startAddress"`
	Quantity     int       `gorm:"column:quantity" json:"quantity"`
	SortOrder    int       `gorm:"column:sort_order" json:"sortOrder"`
	CreateTime   time.Time `gorm:"column:create_time;autoCreateTime" json:"createTime"`
	UpdateTime   time.Time `gorm:"column:update_time;autoUpdateTime" json:"updateTime"`
}

func (RegisterBlock) TableName() string { return "acquisition_register_block" }

type RegisterBlockInput struct {
	ID           int64
	Name         string
	FunctionCode int
	StartAddress int
	Quantity     int
	SortOrder    int
}

type ChannelQuery struct {
	Page     int
	PageSize int
}

type DeviceQuery struct {
	Page      int
	PageSize  int
	ChannelID *int64
	Enabled   *int
}

type Page[T any] struct {
	Records  []T   `json:"records"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
}

// Script is the editable identity; only PublishedVersionID selects runtime source.
type Script struct {
	ID                 int64     `gorm:"column:id;primaryKey" json:"id"`
	Name               string    `gorm:"column:name" json:"name"`
	Description        string    `gorm:"column:description" json:"description"`
	DraftSource        string    `gorm:"column:draft_source" json:"draftSource"`
	PublishedVersionID *int64    `gorm:"column:published_version_id" json:"publishedVersionId"`
	CreateBy           *int64    `gorm:"column:create_by" json:"createBy"`
	UpdateBy           *int64    `gorm:"column:update_by" json:"updateBy"`
	CreateTime         time.Time `gorm:"column:create_time;autoCreateTime" json:"createTime"`
	UpdateTime         time.Time `gorm:"column:update_time;autoUpdateTime" json:"updateTime"`
	Deleted            int       `gorm:"column:deleted" json:"-"`
}

func (Script) TableName() string { return "acquisition_script" }

type ScriptVersion struct {
	ID          int64     `gorm:"column:id;primaryKey" json:"id"`
	ScriptID    int64     `gorm:"column:script_id" json:"scriptId"`
	VersionNo   int       `gorm:"column:version_no" json:"versionNo"`
	Source      string    `gorm:"column:source" json:"source"`
	Checksum    string    `gorm:"column:checksum" json:"checksum"`
	PublishedBy int64     `gorm:"column:published_by" json:"publishedBy"`
	PublishedAt time.Time `gorm:"column:published_at" json:"publishedAt"`
}

func (ScriptVersion) TableName() string { return "acquisition_script_version" }

type ScriptQuery struct {
	Name     string
	Page     int
	PageSize int
}

type ScriptInput struct {
	Name        string
	Description string
	DraftSource string
}

// ScriptView is the management projection of a script identity. Published
// source is exposed only through the immutable version snapshot selected by
// PublishedVersionID.
type ScriptView struct {
	ID                    int64          `json:"id"`
	Name                  string         `json:"name"`
	Description           string         `json:"description"`
	DraftSource           string         `json:"draftSource"`
	PublishedVersionID    *int64         `json:"publishedVersionId"`
	PublishedVersion      *ScriptVersion `json:"publishedVersion,omitempty"`
	DraftMatchesPublished bool           `json:"draftMatchesPublished"`
	BoundDeviceCount      int64          `json:"boundDeviceCount"`
	CreateTime            time.Time      `json:"createTime"`
	UpdateTime            time.Time      `json:"updateTime"`
}

type ScriptValidationError struct {
	Filename string `json:"filename,omitempty"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Message  string `json:"message"`
}

type ScriptValidationResult struct {
	Valid  bool                    `json:"valid"`
	Errors []ScriptValidationError `json:"errors"`
}

type ScriptRuntimeEvent struct {
	Kind       string    `json:"kind"`
	Key        string    `json:"key"`
	Payload    any       `json:"payload"`
	OccurredAt time.Time `json:"occurredAt"`
}

type ScriptRuntimeState struct {
	DeviceID        int64                `json:"deviceId"`
	DeviceName      string               `json:"deviceName,omitempty"`
	ScriptID        int64                `json:"scriptId"`
	ScriptVersionID int64                `json:"scriptVersionId"`
	VersionNo       int                  `json:"versionNo"`
	LastAttemptAt   *time.Time           `json:"lastAttemptAt"`
	LastSuccessAt   *time.Time           `json:"lastSuccessAt"`
	LastError       *string              `json:"lastError"`
	LastErrorType   *string              `json:"lastErrorType"`
	State           map[string]any       `json:"state"`
	Events          []ScriptRuntimeEvent `json:"events"`
}
