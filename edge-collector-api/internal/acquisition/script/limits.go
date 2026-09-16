package script

import "errors"

const (
	defaultMaxSourceBytes         = 262144
	defaultMaxExecutionMs         = 10000
	defaultMaxExecutionSteps      = 100000
	defaultMaxModbusOperations    = 16
	defaultMaxDelayMs             = 1000
	defaultMaxTotalDelayMs        = 2000
	defaultMaxStateBytesPerDevice = 65536
	defaultMaxEventsPerExecution  = 16
	defaultMaxEventsPerDevice     = 100
	defaultMaxEventPayloadBytes   = 65536
	defaultMaxPrintLines          = 100
	defaultMaxPrintLineBytes      = 2048
)

// Limits are the server-side hard limits for one source or invocation.
type Limits struct {
	MaxSourceBytes         int
	MaxExecutionMs         int
	MaxExecutionSteps      uint64
	MaxModbusOperations    int
	MaxDelayMs             int
	MaxTotalDelayMs        int
	MaxStateBytesPerDevice int
	MaxEventsPerExecution  int
	MaxEventsPerDevice     int
	MaxEventPayloadBytes   int
	MaxPrintLines          int
	MaxPrintLineBytes      int
}

// DefaultLimits returns the exact first-version defaults from the Spec.
func DefaultLimits() Limits {
	return Limits{
		MaxSourceBytes:         defaultMaxSourceBytes,
		MaxExecutionMs:         defaultMaxExecutionMs,
		MaxExecutionSteps:      defaultMaxExecutionSteps,
		MaxModbusOperations:    defaultMaxModbusOperations,
		MaxDelayMs:             defaultMaxDelayMs,
		MaxTotalDelayMs:        defaultMaxTotalDelayMs,
		MaxStateBytesPerDevice: defaultMaxStateBytesPerDevice,
		MaxEventsPerExecution:  defaultMaxEventsPerExecution,
		MaxEventsPerDevice:     defaultMaxEventsPerDevice,
		MaxEventPayloadBytes:   defaultMaxEventPayloadBytes,
		MaxPrintLines:          defaultMaxPrintLines,
		MaxPrintLineBytes:      defaultMaxPrintLineBytes,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxSourceBytes <= 0 {
		l.MaxSourceBytes = d.MaxSourceBytes
	}
	if l.MaxExecutionMs <= 0 {
		l.MaxExecutionMs = d.MaxExecutionMs
	}
	if l.MaxExecutionSteps == 0 {
		l.MaxExecutionSteps = d.MaxExecutionSteps
	}
	if l.MaxModbusOperations <= 0 {
		l.MaxModbusOperations = d.MaxModbusOperations
	}
	if l.MaxDelayMs <= 0 {
		l.MaxDelayMs = d.MaxDelayMs
	}
	if l.MaxTotalDelayMs <= 0 {
		l.MaxTotalDelayMs = d.MaxTotalDelayMs
	}
	if l.MaxStateBytesPerDevice <= 0 {
		l.MaxStateBytesPerDevice = d.MaxStateBytesPerDevice
	}
	if l.MaxEventsPerExecution <= 0 {
		l.MaxEventsPerExecution = d.MaxEventsPerExecution
	}
	if l.MaxEventsPerDevice <= 0 {
		l.MaxEventsPerDevice = d.MaxEventsPerDevice
	}
	if l.MaxEventPayloadBytes <= 0 {
		l.MaxEventPayloadBytes = d.MaxEventPayloadBytes
	}
	if l.MaxPrintLines <= 0 {
		l.MaxPrintLines = d.MaxPrintLines
	}
	if l.MaxPrintLineBytes <= 0 {
		l.MaxPrintLineBytes = d.MaxPrintLineBytes
	}
	return l
}

var errModbusOperationLimit = errors.New("Modbus operation limit exceeded")

// NewModbusOperationCounter creates a counter that permits at most limit
// operations.
func NewModbusOperationCounter(limit int) ModbusOperationCounter {
	return &modbusOperationCounter{limit: limit}
}

type modbusOperationCounter struct {
	limit int
	count int
}

func (c *modbusOperationCounter) Consume() error {
	if c.count >= c.limit {
		return errModbusOperationLimit
	}
	c.count++
	return nil
}

func (c *modbusOperationCounter) Count() int { return c.count }
