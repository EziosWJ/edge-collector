package mqtt

import (
	"strings"
	"testing"
)

func TestValidateConfigRejectsBrokerURLUserinfo(t *testing.T) {
	config := DefaultConfig()
	config.BrokerURL = "mqtt://operator:broker-password@example.test:1883"

	err := ValidateConfig(config)
	if err == nil {
		t.Fatal("ValidateConfig() accepted broker URL userinfo")
	}
	if strings.Contains(err.Error(), "broker-password") {
		t.Fatalf("validation error leaked broker password: %v", err)
	}
}

func TestValidateConfigRejectsMalformedTopicPrefix(t *testing.T) {
	for _, topicPrefix := range []string{"edge//site", "edge\nsite", strings.Repeat("e", 513)} {
		config := DefaultConfig()
		config.TopicPrefix = topicPrefix

		if err := ValidateConfig(config); err == nil {
			t.Fatalf("ValidateConfig(%q) accepted malformed topic prefix", topicPrefix)
		}
	}
}
