package script

import (
	"context"
	"sync"
	"time"

	"go.starlark.net/starlark"
)

// ErrorClass is the stable category of an execution or validation failure.
type ErrorClass string

const (
	ErrorClassScriptCompile   ErrorClass = "SCRIPT_COMPILE"
	ErrorClassScriptRuntime   ErrorClass = "SCRIPT_RUNTIME"
	ErrorClassScriptLimit     ErrorClass = "SCRIPT_LIMIT"
	ErrorClassModbusTransport ErrorClass = "MODBUS_TRANSPORT"
	ErrorClassModbusException ErrorClass = "MODBUS_EXCEPTION"
	ErrorClassScriptOutput    ErrorClass = "SCRIPT_OUTPUT"
	ErrorClassCanceled        ErrorClass = "CANCELED"
	ErrorClassHostUnavailable ErrorClass = "HOST_UNAVAILABLE"
)

// ScriptVersion is the immutable source snapshot selected for one device
// cycle. The checksum is part of the compiled-program cache identity. When it
// is empty, Compile derives the SHA-256 checksum from Source.
type ScriptVersion struct {
	ScriptID  int64
	VersionID int64
	VersionNo int
	Source    string
	Checksum  string
}

// Invocation contains device metadata visible through ctx.
type Invocation struct {
	DeviceID  int64
	ChannelID int64
	UnitID    uint8
	Protocol  string
}

// Host is the explicit seam between Starlark and acquisition I/O. Every
// potentially blocking method must honor ctx. A nil Host is allowed for
// state-only or logging-only scripts.
type Host interface {
	RawRegister(functionCode int, address uint16) (uint16, bool)
	ReadHolding(ctx context.Context, address, quantity uint16) ([]uint16, error)
	WriteRegisters(ctx context.Context, address uint16, values []uint16) error
	WriteCoil(ctx context.Context, address uint16, on bool) error
	Delay(ctx context.Context, duration time.Duration) error
}

// PrintEntry is a single controlled print line with runtime metadata.
type PrintEntry struct {
	Message         string
	DeviceID        int64
	ScriptID        int64
	ScriptVersionID int64
	VersionNo       int
	ChannelID       int64
}

// PrintSink receives print output after limits are checked. A nil sink drops
// valid print output.
type PrintSink interface {
	Print(context.Context, PrintEntry)
}

// PrintSinkFunc adapts a function to PrintSink.
type PrintSinkFunc func(context.Context, PrintEntry)

func (f PrintSinkFunc) Print(ctx context.Context, entry PrintEntry) {
	f(ctx, entry)
}

// ModbusOperationCounter is the small seam used to enforce the per-invocation
// Modbus operation budget.
type ModbusOperationCounter interface {
	Consume() error
	Count() int
}

// ModbusOperationCounterFactory lets a future runner provide an equivalent
// counter without exposing it to Starlark.
type ModbusOperationCounterFactory func(limit int) ModbusOperationCounter

// Options configures the runtime. Zero-valued limits use the Spec defaults.
type Options struct {
	Limits                  Limits
	PrintSink               PrintSink
	OperationCounterFactory ModbusOperationCounterFactory
	Now                     func() time.Time
}

// StateScope identifies one device and one immutable script version.
type StateScope struct {
	DeviceID        int64
	ScriptID        int64
	ScriptVersionID int64
}

// Event is buffered and committed only after a successful invocation.
type Event struct {
	Kind    string
	Key     string
	Payload any
	At      time.Time
}

// StateSnapshot is a defensive copy of committed state and bounded events.
type StateSnapshot struct {
	State  map[string]any
	Events []Event
}

// Result reports work performed by one invocation. State and Events are
// populated only from a successful execution.
type Result struct {
	Duration         time.Duration
	Steps            uint64
	ModbusOperations int
	TotalDelayMs     int
	PrintLines       int
	State            map[string]any
	Events           []Event
}

// CompiledScript is an immutable cached program. Each Invoke creates fresh
// globals and a fresh Thread.
type CompiledScript struct {
	version     ScriptVersion
	checksum    string
	sourceBytes int
	program     *starlark.Program
}

// Runtime is the reusable controlled execution module.
type Runtime struct {
	mu   sync.Mutex
	impl *runtimeImplementation
}
