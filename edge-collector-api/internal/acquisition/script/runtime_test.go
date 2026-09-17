package script

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type testHost struct {
	mu sync.Mutex

	raw       map[[2]int]uint16
	rawOK     map[[2]int]bool
	reads     [][2]uint16
	writes    []writeCall
	coils     []coilCall
	delays    []time.Duration
	readErr   error
	writeErr  error
	coilErr   error
	delayErr  error
	delayWait bool
}

type writeCall struct {
	address uint16
	values  []uint16
}

type coilCall struct {
	address uint16
	on      bool
}

func (h *testHost) RawRegister(functionCode int, address uint16) (uint16, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := [2]int{functionCode, int(address)}
	value, ok := h.raw[key]
	if h.rawOK != nil {
		ok = h.rawOK[key]
	}
	return value, ok
}

func (h *testHost) ReadHolding(_ context.Context, address, quantity uint16) ([]uint16, error) {
	h.mu.Lock()
	h.reads = append(h.reads, [2]uint16{address, quantity})
	err := h.readErr
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	values := make([]uint16, quantity)
	for i := range values {
		values[i] = uint16(i + 1)
	}
	return values, nil
}

func (h *testHost) WriteRegisters(_ context.Context, address uint16, values []uint16) error {
	h.mu.Lock()
	h.writes = append(h.writes, writeCall{address: address, values: append([]uint16(nil), values...)})
	err := h.writeErr
	h.mu.Unlock()
	return err
}

func (h *testHost) WriteCoil(_ context.Context, address uint16, on bool) error {
	h.mu.Lock()
	h.coils = append(h.coils, coilCall{address: address, on: on})
	err := h.coilErr
	h.mu.Unlock()
	return err
}

func (h *testHost) Delay(ctx context.Context, duration time.Duration) error {
	h.mu.Lock()
	h.delays = append(h.delays, duration)
	err := h.delayErr
	wait := h.delayWait
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if wait {
		<-ctx.Done()
	}
	return nil
}

type testPrintSink struct {
	mu      sync.Mutex
	entries []PrintEntry
}

func (s *testPrintSink) Print(_ context.Context, entry PrintEntry) {
	s.mu.Lock()
	s.entries = append(s.entries, entry)
	s.mu.Unlock()
}

func testVersion(source string) ScriptVersion {
	return ScriptVersion{
		ScriptID:  11,
		VersionID: 22,
		VersionNo: 3,
		Source:    source,
	}
}

func TestCompileValidationAndLocation(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		message string
		line    int
		column  int
	}{
		{
			name:    "syntax",
			source:  "def after_poll(ctx)\n    pass\n",
			message: "got newline",
			line:    2,
		},
		{
			name:    "missing entrypoint",
			source:  "value = 1\n",
			message: "missing required entrypoint",
			line:    1,
		},
		{
			name:    "wrong signature",
			source:  "def after_poll(ctx, other):\n    pass\n",
			message: "exactly one",
			line:    1,
		},
		{
			name:    "duplicate entrypoint",
			source:  "def after_poll(ctx):\n    pass\n\ndef after_poll(ctx):\n    pass\n",
			message: "exactly once",
			line:    4,
		},
		{
			name:    "load",
			source:  "def after_poll(ctx):\n    load(\"module\", \"name\")\n",
			message: "load()",
			line:    2,
		},
		{
			name:    "dangerous builtin",
			source:  "def after_poll(ctx):\n    dir(ctx)\n",
			message: "introspection",
			line:    2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.source)
			if err == nil {
				t.Fatal("Validate() error = nil")
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Validate() error = %q, want substring %q", err, test.message)
			}
			if ClassOf(err) != ErrorClassScriptCompile {
				t.Fatalf("ClassOf() = %q, want %q", ClassOf(err), ErrorClassScriptCompile)
			}
			classified, ok := err.(*Error)
			if !ok {
				t.Fatalf("error type = %T, want *Error", err)
			}
			if test.line != 0 && classified.Line != test.line {
				t.Fatalf("line = %d, want %d (error %v)", classified.Line, test.line, err)
			}
		})
	}

	if err := Validate("def after_poll(ctx):\n    while True:\n        pass\n"); err == nil || !strings.Contains(err.Error(), "while loops") {
		t.Fatalf("Validate(while) = %v, want disabled while error", err)
	}
	if err := Validate("def after_poll(ctx):\n    missing_name(ctx)\n"); err == nil || ClassOf(err) != ErrorClassScriptCompile {
		t.Fatalf("Validate(undefined) = %v, want compile error", err)
	}
}

func TestProgramCacheChecksumAndFreshGlobals(t *testing.T) {
	runtime := NewRuntime(Options{})
	version := testVersion("counter = []\n\ndef after_poll(ctx):\n    counter.append(1)\n    ctx.state_set(\"count\", len(counter))\n")
	first, err := runtime.Compile(version)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Compile(version)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || runtime.CacheSize() != 1 {
		t.Fatalf("cache pointers/size = %p/%p/%d, want same pointer and size 1", first, second, runtime.CacheSize())
	}
	if first.Checksum() == "" || first.SourceBytes() != len([]byte(version.Source)) {
		t.Fatalf("compiled metadata = checksum %q, bytes %d", first.Checksum(), first.SourceBytes())
	}

	invocation := Invocation{DeviceID: 1, ChannelID: 2, UnitID: 3, Protocol: "MODBUS_TCP"}
	result, err := runtime.Invoke(context.Background(), first, invocation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.State["count"]; got != int64(1) {
		t.Fatalf("first fresh global count = %#v, want 1", got)
	}
	result, err = runtime.Invoke(context.Background(), first, invocation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.State["count"]; got != int64(1) {
		t.Fatalf("second fresh global count = %#v, want 1", got)
	}

	changed := version
	changed.Source += "\n"
	third, err := runtime.Compile(changed)
	if err != nil {
		t.Fatal(err)
	}
	if third == first || runtime.CacheSize() != 2 || third.Checksum() == first.Checksum() {
		t.Fatalf("changed source reused cache: first=%p third=%p size=%d", first, third, runtime.CacheSize())
	}
}

func TestCommandEntrypointUsesSharedStateAndHostBoundary(t *testing.T) {
	runtime := NewRuntime(Options{})
	version := testVersion("state = 0\n\ndef after_poll(ctx):\n    ctx.state_set(\"last\", 1)\n\ndef command(ctx, name, args):\n    ctx.write_registers(10, [args[\"value\"]])\n    ctx.state_set(\"last\", name)\n    return {\"name\": name, \"args\": args}\n")
	compiled, err := runtime.Compile(version)
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.CommandAvailable() {
		t.Fatal("CommandAvailable() = false")
	}
	host := new(testHost)
	if _, err := runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 1, ChannelID: 2, UnitID: 3}, host); err != nil {
		t.Fatalf("after_poll: %v", err)
	}
	result, err := runtime.InvokeCommand(context.Background(), compiled, Invocation{DeviceID: 1, ChannelID: 2, UnitID: 3}, host, "set_value", map[string]any{"value": int64(7)})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	if len(host.writes) != 1 || host.writes[0].address != 10 || len(host.writes[0].values) != 1 || host.writes[0].values[0] != 7 {
		t.Fatalf("writes = %#v", host.writes)
	}
	output, ok := result.Output.(map[string]any)
	if !ok || output["name"] != "set_value" {
		t.Fatalf("command output = %#v", result.Output)
	}
	snapshot := runtime.Snapshot(StateScope{DeviceID: 1, ScriptID: version.ScriptID, ScriptVersionID: version.VersionID})
	if snapshot.State["last"] != "set_value" {
		t.Fatalf("shared state = %#v", snapshot.State)
	}
}

func TestCommandEntrypointIsOptionalButStrictWhenPresent(t *testing.T) {
	withoutCommand, err := CompileSource("def after_poll(ctx):\n    pass\n")
	if err != nil {
		t.Fatal(err)
	}
	if withoutCommand.CommandAvailable() {
		t.Fatal("command should be optional")
	}
	for _, source := range []string{
		"def after_poll(ctx):\n    pass\n\ndef command(ctx, name):\n    pass\n",
		"def after_poll(ctx):\n    pass\n\ncommand = 1\n",
	} {
		if _, err := CompileSource(source); err == nil || !strings.Contains(err.Error(), "command") {
			t.Fatalf("CompileSource(%q) error = %v", source, err)
		}
	}
}

func TestHostAPIStateEventAndPrint(t *testing.T) {
	sink := new(testPrintSink)
	at := time.Date(2026, 9, 16, 1, 2, 3, 0, time.UTC)
	runtime := NewRuntime(Options{PrintSink: sink, Now: func() time.Time { return at }})
	version := testVersion(`def after_poll(ctx):
    code = ctx.raw_register(3, 8166)
    missing = ctx.raw_register(3, 9999)
    values = ctx.read_holding(8121, 2)
    ctx.write_registers(8120, [0])
    ctx.write_coil(618, True)
    ctx.delay(5)
    ctx.state_set("code", code)
    ctx.state_set("missing", missing)
    ctx.state_set("values", values)
    ctx.emit_event("fault_detail", "k1", {"registers": values})
    print("device", ctx.device_id, ctx.protocol)
`)
	compiled, err := runtime.Compile(version)
	if err != nil {
		t.Fatal(err)
	}
	host := &testHost{
		raw: map[[2]int]uint16{{3, 8166}: 7},
		rawOK: map[[2]int]bool{
			{3, 8166}: true,
			{3, 9999}: false,
		},
	}
	result, err := runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 10, ChannelID: 20, UnitID: 4, Protocol: "MODBUS_TCP"}, host)
	if err != nil {
		t.Fatal(err)
	}
	if result.ModbusOperations != 3 || result.TotalDelayMs != 5 || result.PrintLines != 1 {
		t.Fatalf("result counters = %+v", result)
	}
	if got := result.State["code"]; got != int64(7) {
		t.Fatalf("code = %#v", got)
	}
	if got := result.State["missing"]; got != nil {
		t.Fatalf("missing raw register = %#v, want nil", got)
	}
	if got, want := result.State["values"], []any{int64(1), int64(2)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %#v, want %#v", got, want)
	}
	if len(result.Events) != 1 || result.Events[0].Kind != "fault_detail" || !result.Events[0].At.Equal(at) {
		t.Fatalf("events = %#v", result.Events)
	}
	host.mu.Lock()
	if len(host.reads) != 1 || host.reads[0] != [2]uint16{8121, 2} || len(host.writes) != 1 || !reflect.DeepEqual(host.writes[0].values, []uint16{0}) || len(host.coils) != 1 || len(host.delays) != 1 || host.delays[0] != 5*time.Millisecond {
		t.Fatalf("host calls: reads=%v writes=%v coils=%v delays=%v", host.reads, host.writes, host.coils, host.delays)
	}
	host.mu.Unlock()
	sink.mu.Lock()
	if len(sink.entries) != 1 || sink.entries[0].Message != "device 10 MODBUS_TCP" || sink.entries[0].DeviceID != 10 || sink.entries[0].ScriptID != 11 || sink.entries[0].ScriptVersionID != 22 || sink.entries[0].VersionNo != 3 || sink.entries[0].ChannelID != 20 {
		t.Fatalf("print entries = %#v", sink.entries)
	}
	sink.mu.Unlock()

	// The same event key is deduplicated by the committed scope.
	if _, err := runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 10, ChannelID: 20, UnitID: 4, Protocol: "MODBUS_TCP"}, host); err != nil {
		t.Fatal(err)
	}
	snapshot := runtime.Snapshot(StateScope{DeviceID: 10, ScriptID: 11, ScriptVersionID: 22})
	if len(snapshot.Events) != 1 {
		t.Fatalf("deduplicated events = %d, want 1", len(snapshot.Events))
	}
}

func TestStateAndEventOverlayRollsBackAfterModbusFailure(t *testing.T) {
	runtime := NewRuntime(Options{})
	version := testVersion(`def after_poll(ctx):
    ctx.write_registers(100, [42])
    ctx.state_set("value", 2)
    ctx.emit_event("kind", "key", {"ok": True})
    ctx.read_holding(1, 1)
`)
	compiled, err := runtime.Compile(version)
	if err != nil {
		t.Fatal(err)
	}
	host := &testHost{readErr: errors.New("device did not answer")}
	result, err := runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 1}, host)
	if err == nil || ClassOf(err) != ErrorClassModbusTransport {
		t.Fatalf("Invoke() error = %v, class %q", err, ClassOf(err))
	}
	if len(host.writes) != 1 {
		t.Fatalf("write count = %d, want 1 even though state rolls back", len(host.writes))
	}
	if result.State != nil || result.Events != nil {
		t.Fatalf("failed result outputs = %+v", result)
	}
	snapshot := runtime.Snapshot(StateScope{DeviceID: 1, ScriptID: 11, ScriptVersionID: 22})
	if len(snapshot.State) != 0 || len(snapshot.Events) != 0 {
		t.Fatalf("rolled back snapshot = %+v", snapshot)
	}
}

func TestResourceLimits(t *testing.T) {
	t.Run("source", func(t *testing.T) {
		_, err := Compile(testVersion("def after_poll(ctx):\n    pass\n"), Limits{MaxSourceBytes: 8})
		if err == nil || ClassOf(err) != ErrorClassScriptLimit {
			t.Fatalf("source error = %v, class %q", err, ClassOf(err))
		}
	})

	t.Run("steps", func(t *testing.T) {
		runtime := NewRuntime(Options{Limits: Limits{MaxExecutionSteps: 20}})
		compiled, err := runtime.Compile(testVersion("def after_poll(ctx):\n    for i in range(1000):\n        pass\n"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 2}, nil)
		if err == nil || ClassOf(err) != ErrorClassScriptLimit {
			t.Fatalf("step error = %v, class %q", err, ClassOf(err))
		}
	})

	t.Run("wall", func(t *testing.T) {
		runtime := NewRuntime(Options{Limits: Limits{MaxExecutionMs: 10, MaxExecutionSteps: 100000}})
		compiled, err := runtime.Compile(testVersion("def after_poll(ctx):\n    ctx.delay(1000)\n"))
		if err != nil {
			t.Fatal(err)
		}
		host := &testHost{delayWait: true}
		_, err = runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 3}, host)
		if err == nil || ClassOf(err) != ErrorClassScriptLimit {
			t.Fatalf("wall error = %v, class %q", err, ClassOf(err))
		}
	})

	t.Run("modbus operations", func(t *testing.T) {
		runtime := NewRuntime(Options{Limits: Limits{MaxModbusOperations: 1}})
		compiled, err := runtime.Compile(testVersion("def after_poll(ctx):\n    ctx.read_holding(1, 1)\n    ctx.read_holding(2, 1)\n"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 4}, &testHost{})
		if err == nil || ClassOf(err) != ErrorClassScriptLimit {
			t.Fatalf("operation error = %v, class %q", err, ClassOf(err))
		}
	})

	t.Run("delay", func(t *testing.T) {
		runtime := NewRuntime(Options{Limits: Limits{MaxDelayMs: 5, MaxTotalDelayMs: 6}})
		compiled, err := runtime.Compile(testVersion("def after_poll(ctx):\n    ctx.delay(4)\n    ctx.delay(3)\n"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 5}, &testHost{})
		if err == nil || ClassOf(err) != ErrorClassScriptLimit {
			t.Fatalf("delay error = %v, class %q", err, ClassOf(err))
		}
	})

	t.Run("state and event bytes", func(t *testing.T) {
		runtime := NewRuntime(Options{Limits: Limits{MaxStateBytesPerDevice: 12, MaxEventPayloadBytes: 3}})
		compiled, err := runtime.Compile(testVersion("def after_poll(ctx):\n    ctx.state_set(\"x\", \"too long\")\n"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 6}, nil)
		if err == nil || ClassOf(err) != ErrorClassScriptLimit {
			t.Fatalf("state size error = %v, class %q", err, ClassOf(err))
		}

		eventVersion := testVersion("def after_poll(ctx):\n    ctx.emit_event(\"k\", \"v\", \"long\")\n")
		eventCompiled, err := runtime.Compile(eventVersion)
		if err != nil {
			t.Fatal(err)
		}
		_, err = runtime.Invoke(context.Background(), eventCompiled, Invocation{DeviceID: 7}, nil)
		if err == nil || ClassOf(err) != ErrorClassScriptLimit {
			t.Fatalf("event size error = %v, class %q", err, ClassOf(err))
		}
	})

	t.Run("print", func(t *testing.T) {
		sink := new(testPrintSink)
		runtime := NewRuntime(Options{PrintSink: sink, Limits: Limits{MaxPrintLines: 1, MaxPrintLineBytes: 4}})
		compiled, err := runtime.Compile(testVersion("def after_poll(ctx):\n    print(\"ok\")\n    print(\"no\")\n"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 8}, nil)
		if err == nil || ClassOf(err) != ErrorClassScriptLimit {
			t.Fatalf("print error = %v, class %q", err, ClassOf(err))
		}
		sink.mu.Lock()
		defer sink.mu.Unlock()
		if len(sink.entries) != 1 {
			t.Fatalf("print entries = %d, want 1", len(sink.entries))
		}
	})
}

func TestCancellationAndOutputClassification(t *testing.T) {
	runtime := NewRuntime(Options{})
	compiled, err := runtime.Compile(testVersion("def after_poll(ctx):\n    ctx.state_set(\"x\", {1: 2})\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 9}, nil)
	if err == nil || ClassOf(err) != ErrorClassScriptOutput {
		t.Fatalf("output error = %v, class %q", err, ClassOf(err))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runtime.Invoke(ctx, compiled, Invocation{DeviceID: 10}, nil)
	if err == nil || ClassOf(err) != ErrorClassCanceled {
		t.Fatalf("cancel error = %v, class %q", err, ClassOf(err))
	}
}
