package acquisition

import "time"

const (
	DeviceTypeFeedProtector = "FEED_PROTECTOR"
	Enabled                 = 1
	Disabled                = 0
)

type Channel struct {
	ID         int64     `gorm:"column:id;primaryKey" json:"id"`
	Name       string    `gorm:"column:name" json:"name"`
	Port       string    `gorm:"column:port" json:"port"`
	BaudRate   int       `gorm:"column:baud_rate" json:"baudRate"`
	DataBits   int       `gorm:"column:data_bits" json:"dataBits"`
	StopBits   int       `gorm:"column:stop_bits" json:"stopBits"`
	Parity     string    `gorm:"column:parity" json:"parity"`
	TimeoutMS  int       `gorm:"column:timeout_ms" json:"timeoutMs"`
	Enabled    int       `gorm:"column:enabled" json:"enabled"`
	CreateTime time.Time `gorm:"column:create_time;autoCreateTime" json:"createTime"`
	UpdateTime time.Time `gorm:"column:update_time;autoUpdateTime" json:"updateTime"`
	Deleted    int       `gorm:"column:deleted" json:"-"`
}

func (Channel) TableName() string { return "acquisition_channel" }

type Device struct {
	ID               int64     `gorm:"column:id;primaryKey" json:"id"`
	Name             string    `gorm:"column:name" json:"name"`
	DeviceType       string    `gorm:"column:device_type" json:"deviceType"`
	ChannelID        int64     `gorm:"column:channel_id" json:"channelId"`
	SlaveID          uint8     `gorm:"column:slave_id" json:"slaveId"`
	PollIntervalMS   int       `gorm:"column:poll_interval_ms" json:"pollIntervalMs"`
	FailureThreshold int       `gorm:"column:failure_threshold" json:"failureThreshold"`
	Enabled          int       `gorm:"column:enabled" json:"enabled"`
	CreateTime       time.Time `gorm:"column:create_time;autoCreateTime" json:"createTime"`
	UpdateTime       time.Time `gorm:"column:update_time;autoUpdateTime" json:"updateTime"`
	Deleted          int       `gorm:"column:deleted" json:"-"`
}

func (Device) TableName() string { return "acquisition_device" }

type ChannelInput struct {
	Name      string
	Port      string
	BaudRate  int
	DataBits  int
	StopBits  int
	Parity    string
	TimeoutMS int
	Enabled   int
}

type DeviceInput struct {
	Name             string
	DeviceType       string
	ChannelID        int64
	SlaveID          uint8
	PollIntervalMS   int
	FailureThreshold int
	Enabled          int
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
