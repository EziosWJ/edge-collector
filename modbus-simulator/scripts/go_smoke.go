// This harness imports the project's real acquisition loop. The Python runner
// builds it in a temporary module, without editing the backend go.mod or DB.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func checkState(store *acquisition.CurrentStateStore, id int64, holdingFirst, inputFirst uint16) {
	state, ok := store.Get(id)
	if !ok || state.Status != acquisition.StatusOnline || state.LastError != "" || len(state.RegisterBlocks) != 2 {
		panic(fmt.Sprintf("unexpected acquisition state: %+v", state))
	}
	holding, input := state.RegisterBlocks[0], state.RegisterBlocks[1]
	if holding.FunctionCode != acquisition.FunctionCodeReadHoldingRegisters || holding.StartAddress != 0 || holding.Quantity != 2 || !holding.Valid ||
		holding.Values[0] == nil || *holding.Values[0] != holdingFirst || holding.Values[1] == nil || *holding.Values[1] != 125 {
		panic(fmt.Sprintf("unexpected FC03 block: %+v", holding))
	}
	if input.FunctionCode != acquisition.FunctionCodeReadInputRegisters || input.StartAddress != 0 || input.Quantity != 1 || !input.Valid ||
		input.Values[0] == nil || *input.Values[0] != inputFirst {
		panic(fmt.Sprintf("unexpected FC04 block: %+v", input))
	}
	body, err := json.Marshal(state)
	must(err)
	fmt.Printf("PASS real Go acquisition %s\n", body)
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rtu0Alias := envOr("ADR0014_RTU0", "/tmp/modbus-rtu0")
	var runtimeCases []runtimeCase
	for _, alias := range []string{rtu0Alias, envOr("ADR0014_RTU1", "/tmp/modbus-rtu1")} {
		channel := acquisition.Channel{ID: 1, Name: "simulator", Protocol: acquisition.ProtocolModbusRTU,
			SerialConfig: &acquisition.SerialConfig{Port: alias, BaudRate: 9600, DataBits: 8, Parity: "N", StopBits: 1}, TimeoutMS: 150, Enabled: 1}
		devices := []acquisition.Device{{ID: 1, Name: "feeder-1", ChannelID: 1, UnitID: 1,
			DeviceType: acquisition.DeviceTypeFeedProtector, FailureThreshold: 3, Enabled: 1,
			RegisterBlocks: []acquisition.RegisterBlock{
				{ID: 101, Name: "holding", FunctionCode: acquisition.FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 2, SortOrder: 0},
				{ID: 102, Name: "input", FunctionCode: acquisition.FunctionCodeReadInputRegisters, StartAddress: 0, Quantity: 1, SortOrder: 1},
			}}}
		if alias == rtu0Alias {
			devices = append(devices, acquisition.Device{ID: 2, Name: "feeder-2", ChannelID: 1, UnitID: 2,
				DeviceType: acquisition.DeviceTypeFeedProtector, FailureThreshold: 3, Enabled: 1,
				RegisterBlocks: []acquisition.RegisterBlock{
					{ID: 201, Name: "holding", FunctionCode: acquisition.FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 2, SortOrder: 0},
					{ID: 202, Name: "input", FunctionCode: acquisition.FunctionCodeReadInputRegisters, StartAddress: 0, Quantity: 1, SortOrder: 1},
				}})
		}
		session, err := acquisition.NewModbusSessionFactory()(channel, devices[0])
		must(err)
		must(session.Open())
		store := acquisition.NewCurrentStateStore()
		must(acquisition.PollChannelOnce(ctx, channel, devices, session, store))
		inputFirst := uint16(42)
		checkState(store, 1, 3000, inputFirst)
		if len(devices) == 2 {
			checkState(store, 2, 3100, 43)
			devices[0].UnitID = 99 // Unknown unit: real timeout, other unit still collects.
			for i := 0; i < 3; i++ {
				must(acquisition.PollChannelOnce(ctx, channel, devices, session, store))
			}
			state, _ := store.Get(1)
			if state.Status != acquisition.StatusOffline || state.ConsecutiveFailures != 3 || state.LastError == "" {
				panic(fmt.Sprintf("expected offline: %+v", state))
			}
			checkState(store, 2, 3100, 43)
			devices[0].UnitID = 1
			must(acquisition.PollChannelOnce(ctx, channel, devices, session, store))
			checkState(store, 1, 3000, inputFirst)
			fmt.Println("PASS real Go timeout -> OFFLINE, other unit ONLINE, recovery -> ONLINE")
			runtimeCases = append(runtimeCases, runtimeCase{channel: channel, devices: devices})
		}
		must(session.Close())
	}
	if os.Getenv("GO_SMOKE_RT_ONLY") == "1" {
		fmt.Fprintln(os.Stdout, "ALL GO RTU SMOKE CHECKS PASSED")
		return
	}
	for _, test := range []struct {
		protocol string
	}{
		{protocol: acquisition.ProtocolModbusTCP},
		{protocol: acquisition.ProtocolModbusUDP},
		{protocol: acquisition.ProtocolModbusRTUOverUDP},
	} {
		channel := acquisition.Channel{ID: 10, Name: test.protocol, Protocol: test.protocol, TimeoutMS: 1000, Enabled: 1}
		store := acquisition.NewCurrentStateStore()
		devices := make([]acquisition.Device, 0, 2)
		for index, host := range []string{"127.0.0.1", "localhost"} {
			device := acquisition.Device{ID: int64(index + 10), Name: fmt.Sprintf("network-%s", host), ChannelID: 10, UnitID: 1,
				NetworkEndpoint:  &acquisition.NetworkEndpoint{Host: host, Port: map[string]int{acquisition.ProtocolModbusTCP: 1502, acquisition.ProtocolModbusUDP: 1600, acquisition.ProtocolModbusRTUOverUDP: 1700}[test.protocol]},
				FailureThreshold: 3, Enabled: 1, RegisterBlocks: []acquisition.RegisterBlock{
					{ID: int64(index*2 + 100), Name: "holding", FunctionCode: acquisition.FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 2, SortOrder: 0},
					{ID: int64(index*2 + 101), Name: "input", FunctionCode: acquisition.FunctionCodeReadInputRegisters, StartAddress: 0, Quantity: 1, SortOrder: 1},
				}}
			session, err := acquisition.NewModbusSessionFactory()(channel, device)
			must(err)
			must(session.Open())
			must(acquisition.PollChannelOnce(ctx, channel, []acquisition.Device{device}, session, store))
			must(session.Close())
			checkState(store, device.ID, 3000, 42)
			devices = append(devices, device)
		}
		fmt.Printf("PASS real Go acquisition %s devices=%d\n", test.protocol, len(devices))
		runtimeCases = append(runtimeCases, runtimeCase{channel: channel, devices: devices})
	}
	checkRuntimesConcurrently(ctx, runtimeCases)
	checkTCPFailureRecovery(ctx)
	fmt.Fprintln(os.Stdout, "ALL GO SMOKE CHECKS PASSED")
}

type runtimeCase struct {
	channel acquisition.Channel
	devices []acquisition.Device
}

func checkRuntimesConcurrently(parent context.Context, cases []runtimeCase) {
	var wait sync.WaitGroup
	for _, test := range cases {
		test := test
		wait.Add(1)
		go func() {
			defer wait.Done()
			checkRuntime(parent, test.channel, test.devices)
		}()
	}
	wait.Wait()
	fmt.Printf("PASS four-protocol Runtime concurrency cases=%d\n", len(cases))
}

func checkTCPFailureRecovery(parent context.Context) {
	channel := acquisition.Channel{ID: 120, Name: "TCP failure recovery", Protocol: acquisition.ProtocolModbusTCP, TimeoutMS: 200, Enabled: 1}
	blocks := []acquisition.RegisterBlock{
		{ID: 1201, Name: "holding", FunctionCode: acquisition.FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 2, SortOrder: 0},
		{ID: 1202, Name: "input", FunctionCode: acquisition.FunctionCodeReadInputRegisters, StartAddress: 0, Quantity: 1, SortOrder: 1},
	}
	good := acquisition.Device{ID: 1201, Name: "tcp-good", ChannelID: channel.ID, UnitID: 1,
		NetworkEndpoint: &acquisition.NetworkEndpoint{Host: "127.0.0.1", Port: 1502}, FailureThreshold: 1, PollIntervalMS: 100,
		Enabled: 1, RegisterBlocks: blocks}
	failing := good
	failing.ID = 1202
	failing.Name = "tcp-failing"
	failing.NetworkEndpoint = &acquisition.NetworkEndpoint{Host: "127.0.0.1", Port: 1}
	recovered := failing
	recovered.NetworkEndpoint = &acquisition.NetworkEndpoint{Host: "localhost", Port: 1502}
	current := []acquisition.Device{good, failing}
	var endpointMu sync.RWMutex
	useRecoveredEndpoint := false
	loader := func(context.Context) ([]acquisition.Channel, []acquisition.Device, error) {
		endpointMu.RLock()
		defer endpointMu.RUnlock()
		if useRecoveredEndpoint {
			return []acquisition.Channel{channel}, []acquisition.Device{good, recovered}, nil
		}
		return []acquisition.Channel{channel}, current, nil
	}
	store := acquisition.NewCurrentStateStore()
	runtime, err := acquisition.NewRuntime([]acquisition.Channel{channel}, current, store, acquisition.NewModbusSessionFactory(), nil, loader)
	must(err)
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	waitFor := func(condition func() bool, message string) {
		deadline := time.NewTimer(8 * time.Second)
		defer deadline.Stop()
		for {
			if condition() {
				return
			}
			select {
			case err := <-done:
				panic(fmt.Sprintf("runtime stopped before %s: %v", message, err))
			case <-ctx.Done():
				panic(fmt.Sprintf("runtime did not %s: %v", message, ctx.Err()))
			case <-deadline.C:
				panic("runtime did not " + message)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	waitFor(func() bool {
		goodState, goodOK := store.Get(good.ID)
		failedState, failedOK := store.Get(failing.ID)
		channelState, channelOK := store.ChannelState(channel.ID)
		return goodOK && goodState.Status == acquisition.StatusOnline && failedOK && failedState.Status == acquisition.StatusOffline &&
			channelOK && channelState.Status == acquisition.ChannelStatusDegraded
	}, "putting one endpoint offline without blocking its peer")

	endpointMu.Lock()
	useRecoveredEndpoint = true
	endpointMu.Unlock()
	must(runtime.Refresh(ctx))
	waitFor(func() bool {
		failedState, failedOK := store.Get(failing.ID)
		channelState, channelOK := store.ChannelState(channel.ID)
		return failedOK && failedState.Status == acquisition.StatusOnline && channelOK && channelState.Status == acquisition.ChannelStatusOnline
	}, "recovering the TCP endpoint after a hot update")
	cancel()
	if err := <-done; err != context.Canceled {
		panic(fmt.Sprintf("runtime stopped with unexpected error after recovery: %v", err))
	}
	fmt.Println("PASS real TCP failure isolation -> OFFLINE -> endpoint hot-update recovery")
}

func checkRuntime(parent context.Context, channel acquisition.Channel, devices []acquisition.Device) {
	store := acquisition.NewCurrentStateStore()
	runtime, err := acquisition.NewRuntime([]acquisition.Channel{channel}, devices, store, acquisition.NewModbusSessionFactory(), nil)
	must(err)
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	for {
		ready := true
		for _, device := range devices {
			state, ok := store.Get(device.ID)
			if !ok || state.Status != acquisition.StatusOnline {
				ready = false
				break
			}
		}
		channelState, channelReady := store.ChannelState(channel.ID)
		if ready && channelReady && channelState.Status == acquisition.ChannelStatusOnline {
			cancel()
			if err := <-done; err != context.Canceled {
				panic(fmt.Sprintf("runtime stopped with unexpected error: %v", err))
			}
			fmt.Printf("PASS real Runtime/channel-state aggregation %s devices=%d channel=%s\n", channel.Protocol, len(devices), channelState.Status)
			return
		}
		select {
		case err := <-done:
			panic(fmt.Sprintf("runtime stopped before all devices became online: %v", err))
		case <-ctx.Done():
			panic(fmt.Sprintf("runtime did not collect all devices: %v", ctx.Err()))
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
