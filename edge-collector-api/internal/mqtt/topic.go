package mqtt

import (
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidTopicIdentity = errors.New("MQTT_TOPIC_IDENTITY_INVALID")

type TopicBuilder struct {
	prefix string
	edgeID string
}

func NewTopicBuilder(prefix, edgeID string) (TopicBuilder, error) {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" || strings.ContainsAny(prefix, "+#\x00\r\n") || strings.Contains(prefix, "//") || !validIdentitySegment(edgeID) {
		return TopicBuilder{}, ErrInvalidTopicIdentity
	}
	for _, segment := range strings.Split(prefix, "/") {
		if !validIdentitySegment(segment) {
			return TopicBuilder{}, ErrInvalidTopicIdentity
		}
	}
	return TopicBuilder{prefix: prefix, edgeID: edgeID}, nil
}

func (b TopicBuilder) Prefix() string { return b.prefix }

func (b TopicBuilder) EdgeID() string { return b.edgeID }

func (b TopicBuilder) EdgeStatus() string {
	return b.prefix + "/" + b.edgeID + "/status"
}

func (b TopicBuilder) DeviceRaw(deviceID string) (string, error) {
	return b.deviceTopic(deviceID, "raw")
}

func (b TopicBuilder) DeviceEvent(deviceID string) (string, error) {
	return b.deviceTopic(deviceID, "event")
}

func (b TopicBuilder) DeviceStatus(deviceID string) (string, error) {
	return b.deviceTopic(deviceID, "status")
}

func (b TopicBuilder) DeviceCommand(deviceID string) (string, error) {
	return b.deviceTopic(deviceID, "command")
}

func (b TopicBuilder) DeviceCommandResult(deviceID string) (string, error) {
	return b.deviceTopic(deviceID, "command-result")
}

func (b TopicBuilder) CommandSubscription() string {
	return b.prefix + "/" + b.edgeID + "/device/+/command"
}

func (b TopicBuilder) deviceTopic(deviceID, suffix string) (string, error) {
	if !validIdentitySegment(deviceID) {
		return "", ErrInvalidTopicIdentity
	}
	return fmt.Sprintf("%s/%s/device/%s/%s", b.prefix, b.edgeID, deviceID, suffix), nil
}

func (b TopicBuilder) ParseCommandTopic(topic string) (string, error) {
	parts := strings.Split(topic, "/")
	prefixParts := strings.Split(b.prefix, "/")
	if len(parts) != len(prefixParts)+4 {
		return "", ErrInvalidTopicIdentity
	}
	for index, part := range prefixParts {
		if parts[index] != part {
			return "", ErrInvalidTopicIdentity
		}
	}
	base := len(prefixParts)
	if parts[base] != b.edgeID || parts[base+1] != "device" || parts[base+3] != "command" || !validIdentitySegment(parts[base+2]) {
		return "", ErrInvalidTopicIdentity
	}
	return parts[base+2], nil
}
