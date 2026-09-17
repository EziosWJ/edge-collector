package mqtt

import (
	"context"
	"errors"
)

var (
	ErrReliableResultCapacityExhausted = errors.New("RELIABLE_RESULT_CAPACITY_EXHAUSTED")
	ErrCommandConflict                 = errors.New("COMMAND_ID_CONFLICT")
	ErrCommandJournalCapacity          = errors.New("COMMAND_JOURNAL_CAPACITY_EXHAUSTED")
	ErrOutboxMessageInvalid            = errors.New("MQTT_OUTBOX_MESSAGE_INVALID")
	ErrReservationNotFound             = errors.New("MQTT_FINAL_RESERVATION_NOT_FOUND")
	ErrInvalidConfig                   = errors.New("MQTT_CONFIG_INVALID")
	ErrCommandNotFound                 = errors.New("MQTT_COMMAND_NOT_FOUND")
)

func isTerminalCommandStatus(status string) bool {
	switch status {
	case CommandStatusRejected, CommandStatusExpired, CommandStatusSucceeded, CommandStatusFailed:
		return true
	default:
		return false
	}
}

// stableDeliveryErrorCode is the only error representation allowed to cross
// the reliable delivery diagnostic boundary. MQTT libraries and database
// drivers may include connection strings, SQL detail, or stack-like text in
// their errors; management/API/log consumers only need a bounded code.
func stableDeliveryErrorCode(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrMQTTNotConnected):
		return ErrMQTTNotConnected.Error()
	case errors.Is(err, context.Canceled):
		return "CONTEXT_CANCELED"
	case errors.Is(err, context.DeadlineExceeded):
		return "CONTEXT_DEADLINE_EXCEEDED"
	case errors.Is(err, ErrReliableResultCapacityExhausted):
		return ErrReliableResultCapacityExhausted.Error()
	case errors.Is(err, ErrOutboxIngressFull):
		return ErrOutboxIngressFull.Error()
	case errors.Is(err, ErrOutboxMessageInvalid):
		return ErrOutboxMessageInvalid.Error()
	default:
		return "MQTT_DELIVERY_FAILED"
	}
}
