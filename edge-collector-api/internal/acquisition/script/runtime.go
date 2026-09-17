package script

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.starlark.net/starlark"
)

type cacheKey struct {
	scriptID  int64
	versionID int64
	versionNo int
	checksum  string
}

type runtimeImplementation struct {
	limits         Limits
	printSink      PrintSink
	counterFactory ModbusOperationCounterFactory
	now            func() time.Time
	hostTime       func() time.Time
	state          *stateStore

	cacheMu sync.Mutex
	cache   map[cacheKey]*CompiledScript
}

// NewRuntime creates a reusable compiler cache and in-memory script state
// store. The Runtime is safe for concurrent invocations; invocations sharing
// one StateScope are serialized, while different scopes may run in parallel.
func NewRuntime(options Options) *Runtime {
	return &Runtime{impl: newRuntimeImplementation(options)}
}

func newRuntimeImplementation(options Options) *runtimeImplementation {
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	hostTime := options.HostTime
	if hostTime == nil {
		hostTime = time.Now
	}
	counterFactory := options.OperationCounterFactory
	if counterFactory == nil {
		counterFactory = NewModbusOperationCounter
	}
	return &runtimeImplementation{
		limits:         options.Limits.withDefaults(),
		printSink:      options.PrintSink,
		counterFactory: counterFactory,
		now:            now,
		hostTime:       hostTime,
		state:          newStateStore(),
		cache:          make(map[cacheKey]*CompiledScript),
	}
}

func (r *Runtime) implementation() *runtimeImplementation {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.impl == nil {
		r.impl = newRuntimeImplementation(Options{})
	}
	return r.impl
}

// Compile validates and compiles a published source snapshot. Equal
// version/checksum keys return the same immutable CompiledScript pointer.
func (r *Runtime) Compile(version ScriptVersion) (*CompiledScript, error) {
	impl := r.implementation()
	if impl == nil {
		return nil, newClassifiedError(ErrorClassScriptCompile, "nil script runtime", nil)
	}
	checksum := sourceChecksum(version.Source)
	if version.Checksum != "" && !strings.EqualFold(version.Checksum, checksum) {
		return nil, newClassifiedError(ErrorClassScriptCompile, "published checksum does not match source", nil)
	}
	key := makeCacheKey(version)
	impl.cacheMu.Lock()
	if compiled := impl.cache[key]; compiled != nil {
		impl.cacheMu.Unlock()
		return compiled, nil
	}
	impl.cacheMu.Unlock()

	compiled, err := compileVersion(version, impl.limits)
	if err != nil {
		return nil, err
	}
	impl.cacheMu.Lock()
	if existing := impl.cache[key]; existing != nil {
		impl.cacheMu.Unlock()
		return existing, nil
	}
	impl.cache[key] = compiled
	impl.cacheMu.Unlock()
	return compiled, nil
}

// CommandAvailable compiles the immutable source snapshot and reports whether
// it exposes the optional command entrypoint. Compilation errors are returned
// to the caller so intake can reject an unavailable target before queuing I/O.
func (r *Runtime) CommandAvailable(version ScriptVersion) (bool, error) {
	compiled, err := r.Compile(version)
	if err != nil {
		return false, err
	}
	return compiled.CommandAvailable(), nil
}

// Validate compiles source with this runtime's limits without adding it to
// the published-program cache.
func (r *Runtime) Validate(source string) error {
	impl := r.implementation()
	if impl == nil {
		return newClassifiedError(ErrorClassScriptCompile, "nil script runtime", nil)
	}
	_, err := compileVersion(ScriptVersion{Source: source}, impl.limits)
	return err
}

// CacheSize is primarily useful for diagnostics and tests.
func (r *Runtime) CacheSize() int {
	impl := r.implementation()
	if impl == nil {
		return 0
	}
	impl.cacheMu.Lock()
	defer impl.cacheMu.Unlock()
	return len(impl.cache)
}

// Invoke executes one compiled published version for one device cycle.
// State and events are committed only when the complete invocation succeeds.
func (r *Runtime) Invoke(ctx context.Context, compiled *CompiledScript, invocation Invocation, host Host) (Result, error) {
	impl := r.implementation()
	if impl == nil {
		return Result{}, newClassifiedError(ErrorClassScriptRuntime, "nil script runtime", nil)
	}
	if compiled == nil || compiled.program == nil {
		return Result{}, newClassifiedError(ErrorClassScriptCompile, "nil compiled script", nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return impl.invoke(ctx, compiled, invocation, host)
}

// InvokeCommand executes the optional command(ctx, name, args) entrypoint in
// the same state scope as after_poll. The caller remains responsible for the
// acquisition safe boundary; this method only crosses the Starlark/Host seam.
func (r *Runtime) InvokeCommand(ctx context.Context, compiled *CompiledScript, invocation Invocation, host Host, name string, args any) (Result, error) {
	impl := r.implementation()
	if impl == nil {
		return Result{}, newClassifiedError(ErrorClassScriptRuntime, "nil script runtime", nil)
	}
	if compiled == nil || compiled.program == nil {
		return Result{}, newClassifiedError(ErrorClassScriptCompile, "nil compiled script", nil)
	}
	if !compiled.CommandAvailable() {
		return Result{}, newClassifiedError(ErrorClassScriptCompile, "compiled script has no command entrypoint", nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	converted, err := JSONToStarlark(args)
	if err != nil {
		return Result{}, newClassifiedError(ErrorClassScriptOutput, "command arguments are not JSON-compatible: "+err.Error(), err)
	}
	return impl.invokeEntry(ctx, compiled, invocation, host, "command", starlark.Tuple{starlark.String(name), converted}, true)
}

// InvokeVersion compiles or obtains a cached program before invoking it.
func (r *Runtime) InvokeVersion(ctx context.Context, version ScriptVersion, invocation Invocation, host Host) (Result, error) {
	compiled, err := r.Compile(version)
	if err != nil {
		return Result{}, err
	}
	return r.Invoke(ctx, compiled, invocation, host)
}

// Execute is an alias with a verb suited to service adapters.
func (r *Runtime) Execute(ctx context.Context, version ScriptVersion, invocation Invocation, host Host) (Result, error) {
	return r.InvokeVersion(ctx, version, invocation, host)
}

// ExecuteCommand compiles a published version and executes its optional
// command entrypoint.
func (r *Runtime) ExecuteCommand(ctx context.Context, version ScriptVersion, invocation Invocation, host Host, name string, args any) (Result, error) {
	compiled, err := r.Compile(version)
	if err != nil {
		return Result{}, err
	}
	return r.InvokeCommand(ctx, compiled, invocation, host, name, args)
}

// Snapshot returns defensive copies of the committed state and bounded event
// history for a device and published script version.
func (r *Runtime) Snapshot(scope StateScope) StateSnapshot {
	impl := r.implementation()
	if impl == nil {
		return StateSnapshot{State: make(map[string]any)}
	}
	return impl.state.snapshot(scope)
}

// Reset removes committed state and events for a device/version scope.
func (r *Runtime) Reset(scope StateScope) {
	impl := r.implementation()
	if impl != nil {
		impl.state.reset(scope)
	}
}

func (impl *runtimeImplementation) invoke(ctx context.Context, compiled *CompiledScript, invocation Invocation, host Host) (Result, error) {
	return impl.invokeEntry(ctx, compiled, invocation, host, "after_poll", nil, false)
}

func (impl *runtimeImplementation) invokeEntry(ctx context.Context, compiled *CompiledScript, invocation Invocation, host Host, entryName string, entryArgs starlark.Tuple, captureOutput bool) (Result, error) {
	scope := StateScope{
		DeviceID:        invocation.DeviceID,
		ScriptID:        compiled.version.ScriptID,
		ScriptVersionID: compiled.version.VersionID,
	}

	// A version scope is also the transaction serialization boundary. This
	// keeps a read-modify-write state sequence atomic for one device/version.
	unlock := impl.state.lockScope(scope)
	defer unlock()
	if err := ctx.Err(); err != nil {
		return Result{}, classifiedContextError(err)
	}

	start := time.Now()
	executionContext, cancel := context.WithTimeout(ctx, durationMillis(impl.limits.MaxExecutionMs))
	defer cancel()

	var stepLimitHit atomic.Bool
	var wallLimitHit atomic.Bool
	var stopping atomic.Bool
	thread := &starlark.Thread{
		Name: fmt.Sprintf("acquisition-script-%d", invocation.DeviceID),
	}
	thread.SetMaxExecutionSteps(impl.limits.MaxExecutionSteps)
	thread.OnMaxSteps = func(thread *starlark.Thread) {
		stepLimitHit.Store(true)
		thread.Cancel("script execution step limit exceeded")
	}

	watchDone := make(chan struct{})
	var watchWG sync.WaitGroup
	watchWG.Add(1)
	go func() {
		defer watchWG.Done()
		select {
		case <-executionContext.Done():
			if stopping.Load() {
				return
			}
			if ctx.Err() != nil {
				thread.Cancel("context canceled")
				return
			}
			wallLimitHit.Store(true)
			thread.Cancel("script wall-clock limit exceeded")
		case <-watchDone:
		}
	}()

	execution := &invocationState{
		ctx:        executionContext,
		invocation: invocation,
		version:    compiled.version,
		host:       host,
		limits:     impl.limits,
		store:      impl.state,
		scope:      scope,
		state:      impl.state.load(scope).state,
		counter:    impl.counterFactory(impl.limits.MaxModbusOperations),
		now:        impl.now,
		hostTime:   impl.hostTime(),
		printSink:  impl.printSink,
	}
	if execution.counter == nil {
		execution.counter = NewModbusOperationCounter(impl.limits.MaxModbusOperations)
	}

	var invocationErr error
	predeclared := compilePredeclared()
	predeclared["print"] = starlark.NewBuiltin("print", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		return execution.print(args, kwargs)
	})
	globals, err := compiled.program.Init(thread, predeclared)
	if err != nil {
		invocationErr = err
	}
	if invocationErr == nil {
		if err := executionContext.Err(); err != nil {
			invocationErr = err
		}
	}
	if invocationErr == nil {
		entrypoint, ok := globals[entryName]
		if !ok {
			invocationErr = newClassifiedError(ErrorClassScriptCompile, "compiled script has no "+entryName+" entrypoint", nil)
		} else if _, ok := entrypoint.(starlark.Callable); !ok {
			invocationErr = newClassifiedError(ErrorClassScriptCompile, entryName+" is not callable", nil)
		} else {
			callArgs := starlark.Tuple{&contextValue{execution: execution}}
			callArgs = append(callArgs, entryArgs...)
			var returnValue starlark.Value
			returnValue, invocationErr = starlark.Call(thread, entrypoint, callArgs, nil)
			if invocationErr == nil && captureOutput {
				resultOutput, outputErr := CommandOutput(returnValue)
				if outputErr != nil {
					invocationErr = newClassifiedError(ErrorClassScriptOutput, "command output is not JSON-compatible: "+outputErr.Error(), outputErr)
				} else {
					// The result is copied below after execution limits and context
					// checks have completed; no output is exposed on a failed call.
					execution.commandOutput = resultOutput
				}
			}
		}
	}

	parentErr := ctx.Err()
	executionErr := executionContext.Err()
	stopping.Store(true)
	close(watchDone)
	cancel()
	watchWG.Wait()

	result := Result{
		Duration:         time.Since(start),
		Steps:            thread.ExecutionSteps(),
		ModbusOperations: execution.counter.Count(),
		TotalDelayMs:     execution.totalDelayMs,
		PrintLines:       execution.printLines,
		Output:           cloneCommandOutput(execution.commandOutput),
	}
	if parentErr != nil {
		invocationErr = parentErr
	} else if ctx.Err() != nil {
		invocationErr = ctx.Err()
	} else if invocationErr == nil && executionErr != nil && executionErr != context.Canceled {
		// A timeout may be observed just after the Starlark call returned.
		invocationErr = executionErr
	}
	if invocationErr != nil || stepLimitHit.Load() || wallLimitHit.Load() {
		return result, classifyInvocationError(invocationErr, ctx, executionErr, stepLimitHit.Load(), wallLimitHit.Load())
	}

	// The caller's context may be canceled between the earlier check and this
	// commit. Never make an apparently successful state transaction visible in
	// that case.
	if err := ctx.Err(); err != nil {
		return result, classifiedContextError(err)
	}
	impl.state.commit(scope, execution.state, execution.events, impl.limits.MaxEventsPerDevice)
	result.State = cloneState(execution.state)
	result.Events = cloneEvents(execution.events)
	return result, nil
}

func makeCacheKey(version ScriptVersion) cacheKey {
	return cacheKey{
		scriptID:  version.ScriptID,
		versionID: version.VersionID,
		versionNo: version.VersionNo,
		checksum:  sourceChecksum(version.Source),
	}
}

func durationMillis(milliseconds int) time.Duration {
	const maxDurationMillis = int64((1<<63 - 1) / int64(time.Millisecond))
	if int64(milliseconds) > maxDurationMillis {
		return time.Duration(1<<63 - 1)
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func classifyInvocationError(err error, parent context.Context, executionErr error, stepLimit, wallLimit bool) *Error {
	if parent != nil && parent.Err() != nil {
		return positionedClassifiedError(ErrorClassCanceled, "script context canceled", err)
	}
	if stepLimit {
		return positionedClassifiedError(ErrorClassScriptLimit, "script execution step limit exceeded", err)
	}
	if wallLimit || errors.Is(executionErr, context.DeadlineExceeded) {
		return positionedClassifiedError(ErrorClassScriptLimit, "script wall-clock execution limit exceeded", err)
	}
	if errors.Is(err, context.Canceled) {
		return positionedClassifiedError(ErrorClassCanceled, "script context canceled", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return positionedClassifiedError(ErrorClassScriptLimit, "script wall-clock execution limit exceeded", err)
	}
	if err == nil {
		return newClassifiedError(ErrorClassScriptRuntime, "script execution failed", nil)
	}
	return classifyRuntimeError(err)
}

func classifiedContextError(err error) *Error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return newClassifiedError(ErrorClassCanceled, "script context canceled", err)
	}
	return newClassifiedError(ErrorClassCanceled, err.Error(), err)
}

func positionedClassifiedError(class ErrorClass, message string, cause error) *Error {
	if cause == nil {
		return newClassifiedError(class, message, nil)
	}
	filename, line, column := runtimeErrorPosition(cause)
	return newPositionedError(class, message, filename, line, column, cause)
}

// Version returns a defensive copy of the immutable source identity.
func (c *CompiledScript) Version() ScriptVersion {
	if c == nil {
		return ScriptVersion{}
	}
	return c.version
}

func (c *CompiledScript) Checksum() string {
	if c == nil {
		return ""
	}
	return c.checksum
}

func (c *CompiledScript) SourceBytes() int {
	if c == nil {
		return 0
	}
	return c.sourceBytes
}

// CommandAvailable reports whether the immutable published program exposes
// the optional remote-control entrypoint.
func (c *CompiledScript) CommandAvailable() bool {
	return c != nil && c.hasCommand
}
