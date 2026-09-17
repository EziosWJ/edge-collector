package mqtt

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

func ValidateConfig(c Config) error {
	var errs []error
	if c.ID != 0 && c.ID != ConfigID {
		errs = append(errs, errors.New("mqtt config id must be 1"))
	}
	if c.EdgeID == "" || len([]byte(c.EdgeID)) > 128 || !validIdentitySegment(c.EdgeID) {
		errs = append(errs, errors.New("mqtt.edge_id must be a non-empty topic identity segment"))
	}
	if err := validateBrokerURL(c.BrokerURL); err != nil {
		errs = append(errs, err)
	}
	if c.ProtocolVersion != ProtocolMQTT5 && c.ProtocolVersion != ProtocolMQTT311 {
		errs = append(errs, errors.New("mqtt.protocol_version must be MQTT_5 or MQTT_3_1_1"))
	}
	if c.ClientID == "" || len([]byte(c.ClientID)) > 256 || strings.ContainsAny(c.ClientID, "\x00\r\n") {
		errs = append(errs, errors.New("mqtt.client_id must be a non-empty valid client identifier"))
	}
	if len([]byte(c.Username)) > 256 {
		errs = append(errs, errors.New("mqtt.username must be at most 256 bytes"))
	}
	if c.KeepAliveSeconds < 5 || c.KeepAliveSeconds > 3600 {
		errs = append(errs, errors.New("mqtt.keep_alive_seconds must be between 5 and 3600"))
	}
	if c.ConnectTimeoutMS < 100 || c.ConnectTimeoutMS > 120000 {
		errs = append(errs, errors.New("mqtt.connect_timeout_ms must be between 100 and 120000"))
	}
	if c.ReconnectMinMS < 100 || c.ReconnectMinMS > 60000 {
		errs = append(errs, errors.New("mqtt.reconnect_min_ms must be between 100 and 60000"))
	}
	if c.ReconnectMaxMS < c.ReconnectMinMS || c.ReconnectMaxMS > 600000 {
		errs = append(errs, errors.New("mqtt.reconnect_max_ms must be >= reconnect_min_ms and <= 600000"))
	}
	if c.TopicPrefix == "" || len([]byte(c.TopicPrefix)) > 512 || strings.ContainsAny(c.TopicPrefix, "+#\x00\r\n") || strings.Contains(c.TopicPrefix, "//") || strings.HasPrefix(c.TopicPrefix, "/") || strings.HasSuffix(c.TopicPrefix, "/") {
		errs = append(errs, errors.New("mqtt.topic_prefix must be non-empty and contain no wildcard or empty topic segment"))
	}
	if c.RawPublishIntervalMS < 50 || c.RawPublishIntervalMS > 3600000 {
		errs = append(errs, errors.New("mqtt.raw_publish_interval_ms must be between 50 and 3600000"))
	}
	if c.OutboxMaxRows < 1 || c.OutboxMaxRows > 1000000 {
		errs = append(errs, errors.New("mqtt.outbox_max_rows must be between 1 and 1000000"))
	}
	if c.OutboxMaxBytes < 1024 || c.OutboxMaxBytes > 1024*1024*1024 {
		errs = append(errs, errors.New("mqtt.outbox_max_bytes must be between 1024 and 1GiB"))
	}
	if c.OutboxRetentionDays < 1 || c.OutboxRetentionDays > 3650 {
		errs = append(errs, errors.New("mqtt.outbox_retention_days must be between 1 and 3650"))
	}
	if c.CommandJournalRetentionDays < 1 || c.CommandJournalRetentionDays > 3650 {
		errs = append(errs, errors.New("mqtt.command_journal_retention_days must be between 1 and 3650"))
	}
	if c.CommandJournalMaxRows < 1 || c.CommandJournalMaxRows > 1000000 {
		errs = append(errs, errors.New("mqtt.command_journal_max_rows must be between 1 and 1000000"))
	}
	if c.CommandQueueCapacity < 1 || c.CommandQueueCapacity > 100000 {
		errs = append(errs, errors.New("mqtt.command_queue_capacity must be between 1 and 100000"))
	}
	if c.CommandPollFairness < 1 || c.CommandPollFairness > 100 {
		errs = append(errs, errors.New("mqtt.command_poll_fairness must be between 1 and 100"))
	}
	return errors.Join(errs...)
}

func validateBrokerURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return errors.New("mqtt.broker_url must be a broker URL with host")
	}
	if parsed.User != nil {
		return errors.New("mqtt.broker_url must not contain user information")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "mqtt", "mqtts", "tcp", "tls", "ssl", "ws", "wss":
		return nil
	default:
		return errors.New("mqtt.broker_url scheme must be mqtt/mqtts/tcp/tls/ssl/ws/wss")
	}
}

func validIdentitySegment(value string) bool {
	return value != "" && !strings.ContainsAny(value, "/+#") && !strings.ContainsAny(value, "\x00\r\n")
}

func validateSecretAction(action SecretAction, value string, field string) error {
	switch action {
	case SecretKeep, SecretClear:
		if value != "" {
			return fmt.Errorf("%s must be empty for %s", field, action)
		}
	case SecretSet:
		if value == "" {
			return fmt.Errorf("%s is required for set", field)
		}
	default:
		return fmt.Errorf("%s action must be keep, set, or clear", field)
	}
	return nil
}
