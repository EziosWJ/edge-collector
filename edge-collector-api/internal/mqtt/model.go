package mqtt

import "time"

const (
	ProtocolMQTT5   = "MQTT_5"
	ProtocolMQTT311 = "MQTT_3_1_1"

	ConfigID int64 = 1

	DefaultKeepAliveSeconds             = 30
	DefaultConnectTimeoutMS             = 10000
	DefaultReconnectMinMS               = 1000
	DefaultReconnectMaxMS               = 60000
	DefaultTopicPrefix                  = "edge"
	DefaultRawPublishIntervalMS         = 1000
	DefaultOutboxMaxRows                = 10000
	DefaultOutboxMaxBytes         int64 = 64 * 1024 * 1024
	DefaultOutboxRetentionDays          = 7
	DefaultJournalRetentionDays         = 7
	DefaultJournalMaxRows               = 10000
	DefaultCommandQueueCapacity         = 32
	DefaultCommandPollFairness          = 1
	DefaultCommandPayloadBytes          = 256 * 1024
	DefaultResultReservationBytes       = 256 * 1024

	OutboxMessageTypeEvent           = "EVENT"
	OutboxMessageTypeCommandAccepted = "COMMAND_ACCEPTED"
	OutboxMessageTypeCommandResult   = "COMMAND_RESULT"

	OutboxPriorityCommandFinal  = 100
	OutboxPriorityReliableEvent = 80
	OutboxPriorityCommandNotice = 60

	CommandStatusAccepted  = "ACCEPTED"
	CommandStatusRejected  = "REJECTED"
	CommandStatusExpired   = "EXPIRED"
	CommandStatusRunning   = "RUNNING"
	CommandStatusSucceeded = "SUCCEEDED"
	CommandStatusFailed    = "FAILED"
)

// Config is the single logical MQTT configuration persisted by the service.
// Secret values are intentionally represented only by ciphertext and boolean
// configured flags; the API projection never exposes either ciphertext.
type Config struct {
	ID                          int64     `gorm:"column:id;primaryKey"`
	Enabled                     int       `gorm:"column:enabled"`
	EdgeID                      string    `gorm:"column:edge_id"`
	BrokerURL                   string    `gorm:"column:broker_url"`
	ProtocolVersion             string    `gorm:"column:protocol_version"`
	ClientID                    string    `gorm:"column:client_id"`
	Username                    string    `gorm:"column:username"`
	PasswordCiphertext          *string   `gorm:"column:password_ciphertext"`
	TLSEnabled                  int       `gorm:"column:tls_enabled"`
	CACertificate               string    `gorm:"column:ca_certificate"`
	ClientCertificate           string    `gorm:"column:client_certificate"`
	ClientPrivateKeyCiphertext  *string   `gorm:"column:client_private_key_ciphertext"`
	KeepAliveSeconds            int       `gorm:"column:keep_alive_seconds"`
	ConnectTimeoutMS            int       `gorm:"column:connect_timeout_ms"`
	ReconnectMinMS              int       `gorm:"column:reconnect_min_ms"`
	ReconnectMaxMS              int       `gorm:"column:reconnect_max_ms"`
	TopicPrefix                 string    `gorm:"column:topic_prefix"`
	RawPublishIntervalMS        int       `gorm:"column:raw_publish_interval_ms"`
	OutboxMaxRows               int       `gorm:"column:outbox_max_rows"`
	OutboxMaxBytes              int64     `gorm:"column:outbox_max_bytes"`
	OutboxRetentionDays         int       `gorm:"column:outbox_retention_days"`
	CommandJournalRetentionDays int       `gorm:"column:command_journal_retention_days"`
	CommandJournalMaxRows       int       `gorm:"column:command_journal_max_rows"`
	CommandQueueCapacity        int       `gorm:"column:command_queue_capacity"`
	CommandPollFairness         int       `gorm:"column:command_poll_fairness"`
	CreateTime                  time.Time `gorm:"column:create_time"`
	UpdateTime                  time.Time `gorm:"column:update_time"`
}

func (Config) TableName() string { return "mqtt_config" }

type ConfigView struct {
	Enabled                     bool   `json:"enabled"`
	EdgeID                      string `json:"edgeId"`
	BrokerURL                   string `json:"brokerUrl"`
	ProtocolVersion             string `json:"protocolVersion"`
	ClientID                    string `json:"clientId"`
	Username                    string `json:"username"`
	PasswordConfigured          bool   `json:"passwordConfigured"`
	TLSEnabled                  bool   `json:"tlsEnabled"`
	CACertificate               string `json:"caCertificate"`
	ClientCertificate           string `json:"clientCertificate"`
	ClientPrivateKeyConfigured  bool   `json:"clientPrivateKeyConfigured"`
	KeepAliveSeconds            int    `json:"keepAliveSeconds"`
	ConnectTimeoutMS            int    `json:"connectTimeoutMs"`
	ReconnectMinMS              int    `json:"reconnectMinMs"`
	ReconnectMaxMS              int    `json:"reconnectMaxMs"`
	TopicPrefix                 string `json:"topicPrefix"`
	RawPublishIntervalMS        int    `json:"rawPublishIntervalMs"`
	OutboxMaxRows               int    `json:"outboxMaxRows"`
	OutboxMaxBytes              int64  `json:"outboxMaxBytes"`
	OutboxRetentionDays         int    `json:"outboxRetentionDays"`
	CommandJournalRetentionDays int    `json:"commandJournalRetentionDays"`
	CommandJournalMaxRows       int    `json:"commandJournalMaxRows"`
	CommandQueueCapacity        int    `json:"commandQueueCapacity"`
	CommandPollFairness         int    `json:"commandPollFairness"`
}

type SecretAction string

const (
	SecretKeep  SecretAction = "keep"
	SecretSet   SecretAction = "set"
	SecretClear SecretAction = "clear"
)

// ConfigInput separates secret intent from the value. A keep request carries
// no secret value; a masked API display value can therefore never be written
// back as a new credential.
type ConfigInput struct {
	Enabled                     bool
	EdgeID                      string
	BrokerURL                   string
	ProtocolVersion             string
	ClientID                    string
	Username                    string
	PasswordAction              SecretAction
	Password                    string
	TLSEnabled                  bool
	CACertificate               string
	ClientCertificate           string
	ClientPrivateKeyAction      SecretAction
	ClientPrivateKey            string
	KeepAliveSeconds            int
	ConnectTimeoutMS            int
	ReconnectMinMS              int
	ReconnectMaxMS              int
	TopicPrefix                 string
	RawPublishIntervalMS        int
	OutboxMaxRows               int
	OutboxMaxBytes              int64
	OutboxRetentionDays         int
	CommandJournalRetentionDays int
	CommandJournalMaxRows       int
	CommandQueueCapacity        int
	CommandPollFairness         int
}

type OutboxMessage struct {
	ID            int64      `gorm:"column:id;primaryKey"`
	MessageID     string     `gorm:"column:message_id;uniqueIndex"`
	MessageType   string     `gorm:"column:message_type"`
	CommandID     *string    `gorm:"column:command_id"`
	Topic         string     `gorm:"column:topic"`
	QoS           int        `gorm:"column:qos"`
	Retain        int        `gorm:"column:retain"`
	Payload       string     `gorm:"column:payload"`
	PayloadBytes  int64      `gorm:"column:payload_bytes"`
	Priority      int        `gorm:"column:priority"`
	CreatedAt     time.Time  `gorm:"column:created_at"`
	ExpiresAt     *time.Time `gorm:"column:expires_at"`
	AttemptCount  int        `gorm:"column:attempt_count"`
	LastAttemptAt *time.Time `gorm:"column:last_attempt_at"`
	LastError     *string    `gorm:"column:last_error"`
}

func (OutboxMessage) TableName() string { return "mqtt_outbox" }

type OutboxStats struct {
	Rows      int64      `json:"rows"`
	Bytes     int64      `json:"bytes"`
	OldestAt  *time.Time `json:"oldestAt"`
	LastError *string    `json:"lastError"`
}

// OutboxStatsView is the management projection of durable outbox health. Age
// is expressed in whole seconds so the HTTP contract remains an ordinary JSON
// scalar and does not expose database implementation details.
type OutboxStatsView struct {
	Rows            int64   `json:"rows"`
	Bytes           int64   `json:"bytes"`
	OldestAge       int64   `json:"oldestAge"`
	LastError       *string `json:"lastError,omitempty"`
	MaxRows         int     `json:"maxRows"`
	MaxBytes        int64   `json:"maxBytes"`
	RowUtilization  float64 `json:"rowUtilization"`
	ByteUtilization float64 `json:"byteUtilization"`
}

// RuntimeStateView combines the connector state with the latest-state and
// durable outbox health needed by the management page.
type RuntimeStateView struct {
	RuntimeSnapshot
	PendingLatestCount int             `json:"pendingLatestCount"`
	Outbox             OutboxStatsView `json:"outbox"`
}

type TestConnectionView struct {
	Success          bool `json:"success"`
	RuntimeConnected bool `json:"runtimeConnected"`
}

type FinalReservation struct {
	CommandID     string    `gorm:"column:command_id;primaryKey"`
	ReservedRows  int       `gorm:"column:reserved_rows"`
	ReservedBytes int64     `gorm:"column:reserved_bytes"`
	AcceptedBytes int64     `gorm:"column:accepted_bytes"`
	FinalBytes    int64     `gorm:"column:final_bytes"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

func (FinalReservation) TableName() string { return "mqtt_outbox_reservation" }

type CommandJournal struct {
	CommandID      string     `gorm:"column:command_id;primaryKey" json:"commandId"`
	DeviceID       string     `gorm:"column:device_id" json:"deviceId"`
	CommandName    string     `gorm:"column:command_name" json:"name"`
	PayloadHash    string     `gorm:"column:payload_hash" json:"-"`
	CommandPayload string     `gorm:"column:command_payload" json:"-"`
	ReceivedAt     time.Time  `gorm:"column:received_at" json:"receivedAt"`
	IssuedAt       time.Time  `gorm:"column:issued_at" json:"issuedAt"`
	ExpiresAt      time.Time  `gorm:"column:expires_at" json:"expiresAt"`
	Status         string     `gorm:"column:status" json:"status"`
	StartedAt      *time.Time `gorm:"column:started_at" json:"startedAt"`
	CompletedAt    *time.Time `gorm:"column:completed_at" json:"completedAt"`
	ResultPayload  string     `gorm:"column:result_payload" json:"-"`
	ErrorType      *string    `gorm:"column:error_type" json:"errorType,omitempty"`
	ErrorMessage   *string    `gorm:"column:error_message" json:"errorMessage,omitempty"`
}

func (CommandJournal) TableName() string { return "mqtt_command_journal" }

type CommandJournalView struct {
	CommandID    string     `json:"commandId"`
	DeviceID     string     `json:"deviceId"`
	CommandName  string     `json:"name"`
	Status       string     `json:"status"`
	ReceivedAt   time.Time  `json:"receivedAt"`
	IssuedAt     time.Time  `json:"issuedAt"`
	ExpiresAt    time.Time  `json:"expiresAt"`
	StartedAt    *time.Time `json:"startedAt"`
	CompletedAt  *time.Time `json:"completedAt"`
	ErrorType    *string    `json:"errorType,omitempty"`
	ErrorMessage *string    `json:"errorMessage,omitempty"`
}

type CommandJournalQuery struct {
	Page      int
	PageSize  int
	Status    string
	DeviceID  string
	CommandID string
	Name      string
}

type Page[T any] struct {
	Records  []T   `json:"records"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
}

func DefaultConfig() Config {
	return Config{
		ID: ConfigID, Enabled: 0, EdgeID: "edge-01", BrokerURL: "mqtt://127.0.0.1:1883",
		ProtocolVersion: ProtocolMQTT5, ClientID: "edge-collector", TLSEnabled: 0,
		KeepAliveSeconds: DefaultKeepAliveSeconds, ConnectTimeoutMS: DefaultConnectTimeoutMS,
		ReconnectMinMS: DefaultReconnectMinMS, ReconnectMaxMS: DefaultReconnectMaxMS,
		TopicPrefix: DefaultTopicPrefix, RawPublishIntervalMS: DefaultRawPublishIntervalMS,
		OutboxMaxRows: DefaultOutboxMaxRows, OutboxMaxBytes: DefaultOutboxMaxBytes,
		OutboxRetentionDays:         DefaultOutboxRetentionDays,
		CommandJournalRetentionDays: DefaultJournalRetentionDays,
		CommandJournalMaxRows:       DefaultJournalMaxRows, CommandQueueCapacity: DefaultCommandQueueCapacity,
		CommandPollFairness: DefaultCommandPollFairness,
	}
}

func (c Config) View() ConfigView {
	return ConfigView{
		Enabled: c.Enabled == 1, EdgeID: c.EdgeID, BrokerURL: c.BrokerURL,
		ProtocolVersion: c.ProtocolVersion, ClientID: c.ClientID, Username: c.Username,
		PasswordConfigured: c.PasswordCiphertext != nil && *c.PasswordCiphertext != "",
		TLSEnabled:         c.TLSEnabled == 1, CACertificate: c.CACertificate,
		ClientCertificate:          c.ClientCertificate,
		ClientPrivateKeyConfigured: c.ClientPrivateKeyCiphertext != nil && *c.ClientPrivateKeyCiphertext != "",
		KeepAliveSeconds:           c.KeepAliveSeconds, ConnectTimeoutMS: c.ConnectTimeoutMS,
		ReconnectMinMS: c.ReconnectMinMS, ReconnectMaxMS: c.ReconnectMaxMS,
		TopicPrefix: c.TopicPrefix, RawPublishIntervalMS: c.RawPublishIntervalMS,
		OutboxMaxRows: c.OutboxMaxRows, OutboxMaxBytes: c.OutboxMaxBytes,
		OutboxRetentionDays:         c.OutboxRetentionDays,
		CommandJournalRetentionDays: c.CommandJournalRetentionDays,
		CommandJournalMaxRows:       c.CommandJournalMaxRows, CommandQueueCapacity: c.CommandQueueCapacity,
		CommandPollFairness: c.CommandPollFairness,
	}
}
