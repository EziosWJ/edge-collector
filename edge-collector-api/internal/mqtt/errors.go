package mqtt

import "errors"

var (
	ErrReliableResultCapacityExhausted = errors.New("RELIABLE_RESULT_CAPACITY_EXHAUSTED")
	ErrCommandConflict                 = errors.New("COMMAND_ID_PAYLOAD_CONFLICT")
	ErrCommandJournalCapacity          = errors.New("COMMAND_JOURNAL_CAPACITY_EXHAUSTED")
	ErrOutboxMessageInvalid            = errors.New("MQTT_OUTBOX_MESSAGE_INVALID")
	ErrReservationNotFound             = errors.New("MQTT_FINAL_RESERVATION_NOT_FOUND")
)

func isTerminalCommandStatus(status string) bool {
	switch status {
	case CommandStatusRejected, CommandStatusExpired, CommandStatusSucceeded, CommandStatusFailed:
		return true
	default:
		return false
	}
}
