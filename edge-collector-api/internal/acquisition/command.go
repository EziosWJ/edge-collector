package acquisition

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition/script"
)

var (
	ErrCommandQueueFull         = errors.New("COMMAND_QUEUE_FULL")
	ErrCommandTargetUnavailable = errors.New("COMMAND_TARGET_UNAVAILABLE")
	ErrCommandUnavailable       = errors.New("COMMAND_ENTRYPOINT_UNAVAILABLE")
	ErrCommandRuntimeStopped    = errors.New("COMMAND_RUNTIME_STOPPED")
)

// CommandRequest is the transport-neutral command invocation handed from
// MQTT intake to acquisition. It deliberately contains no Modbus session.
type CommandRequest struct {
	CommandID string
	DeviceID  int64
	Name      string
	Args      any
}

type CommandExecutionResult struct {
	Version script.ScriptVersion
	Result  script.Result
	Err     error
}

type CommandFuture struct {
	done <-chan CommandExecutionResult
}

func (f *CommandFuture) Wait(ctx context.Context) (CommandExecutionResult, error) {
	if f == nil || f.done == nil {
		return CommandExecutionResult{}, ErrCommandRuntimeStopped
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case result := <-f.done:
		return result, result.Err
	case <-ctx.Done():
		return CommandExecutionResult{}, ctx.Err()
	}
}

type queuedCommand struct {
	request CommandRequest
	done    chan CommandExecutionResult
}

type commandQueue struct {
	mu       sync.Mutex
	items    []queuedCommand
	capacity int
}

func newCommandQueue(capacity int) *commandQueue {
	if capacity < 1 {
		capacity = 32
	}
	return &commandQueue{capacity: capacity}
}

func (q *commandQueue) SetCapacity(capacity int) {
	if capacity < 1 {
		return
	}
	q.mu.Lock()
	q.capacity = capacity
	q.mu.Unlock()
}

func (q *commandQueue) Enqueue(command queuedCommand) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) >= q.capacity {
		return ErrCommandQueueFull
	}
	q.items = append(q.items, command)
	return nil
}

func (q *commandQueue) Dequeue() (queuedCommand, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return queuedCommand{}, false
	}
	command := q.items[0]
	copy(q.items, q.items[1:])
	q.items = q.items[:len(q.items)-1]
	return command, true
}

func (q *commandQueue) DequeueForDevice(deviceID int64) (queuedCommand, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for index, item := range q.items {
		if item.request.DeviceID != deviceID {
			continue
		}
		copy(q.items[index:], q.items[index+1:])
		q.items = q.items[:len(q.items)-1]
		return item, true
	}
	return queuedCommand{}, false
}

func (q *commandQueue) Drain() []queuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := append([]queuedCommand(nil), q.items...)
	q.items = nil
	return items
}

func (r *Runtime) SetCommandQueueConfig(capacity, fairness int) {
	r.mu.Lock()
	if capacity >= 1 {
		r.commandQueueCapacity = capacity
	}
	if fairness >= 1 {
		r.commandPollFairness = fairness
	}
	r.mu.Unlock()
	if capacity >= 1 {
		r.runnersMu.RLock()
		for _, runner := range r.runners {
			runner.SetCommandQueueCapacity(capacity)
		}
		r.runnersMu.RUnlock()
	}
}

// ValidateCommandTarget checks the current committed acquisition snapshot and
// published version. It is intentionally read-only and performs no Modbus I/O.
func (r *Runtime) ValidateCommandTarget(ctx context.Context, deviceID int64, name string) (Device, ScriptVersion, error) {
	if deviceID < 1 || name == "" {
		return Device{}, ScriptVersion{}, ErrCommandTargetUnavailable
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return Device{}, ScriptVersion{}, err
		}
	}
	r.mu.Lock()
	devices := cloneDevices(r.devices)
	versions := cloneScriptVersions(r.scriptVersions)
	scripts := r.scripts
	started := r.started
	r.mu.Unlock()
	if !started {
		return Device{}, ScriptVersion{}, ErrCommandRuntimeStopped
	}
	var device Device
	for _, candidate := range devices {
		if candidate.ID == deviceID && candidate.Enabled == Enabled {
			device = candidate
			break
		}
	}
	if device.ID == 0 {
		return Device{}, ScriptVersion{}, ErrCommandTargetUnavailable
	}
	version, ok := versions[deviceID]
	if !ok || scripts.Executor == nil {
		return Device{}, ScriptVersion{}, ErrCommandUnavailable
	}
	if capability, ok := scripts.Executor.(ScriptCommandCapability); ok {
		available, err := capability.CommandAvailable(ctx, toScriptVersion(version))
		if err != nil {
			return Device{}, ScriptVersion{}, err
		}
		if !available {
			return Device{}, ScriptVersion{}, ErrCommandUnavailable
		}
	} else if _, ok := scripts.Executor.(ScriptCommandExecutor); !ok {
		return Device{}, ScriptVersion{}, ErrCommandUnavailable
	}
	return device, version, nil
}

// EnqueueCommand places a command into the channel runner's bounded queue.
// The runner owns the eventual session and executes it only after a complete
// acquisition cycle has reached its safe boundary.
func (r *Runtime) EnqueueCommand(ctx context.Context, request CommandRequest) (*CommandFuture, error) {
	device, _, err := r.ValidateCommandTarget(ctx, request.DeviceID, request.Name)
	if err != nil {
		return nil, err
	}
	r.runnersMu.RLock()
	runner := r.runners[device.ChannelID]
	r.runnersMu.RUnlock()
	if runner == nil {
		return nil, ErrCommandTargetUnavailable
	}
	done := make(chan CommandExecutionResult, 1)
	future := &CommandFuture{done: done}
	if err := runner.EnqueueCommand(queuedCommand{request: request, done: done}); err != nil {
		return nil, err
	}
	return future, nil
}

func resolveCommandResult(done chan CommandExecutionResult, result CommandExecutionResult) {
	select {
	case done <- result:
	default:
	}
}

func (r *Runtime) commandQueueCapacityValue() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.commandQueueCapacity < 1 {
		return 32
	}
	return r.commandQueueCapacity
}

func (r *Runtime) commandPollFairnessValue() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.commandPollFairness < 1 {
		return 1
	}
	return r.commandPollFairness
}

func toScriptVersion(version ScriptVersion) script.ScriptVersion {
	return script.ScriptVersion{ScriptID: version.ScriptID, VersionID: version.ID, VersionNo: version.VersionNo, Source: version.Source, Checksum: version.Checksum}
}

func fromScriptVersion(version script.ScriptVersion) ScriptVersion {
	return ScriptVersion{ID: version.VersionID, ScriptID: version.ScriptID, VersionNo: version.VersionNo, Source: version.Source, Checksum: version.Checksum}
}

func commandExecutionError(message string, err error) error {
	if err == nil {
		return errors.New(message)
	}
	return fmt.Errorf("%s: %w", message, err)
}
