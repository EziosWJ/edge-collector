// This harness imports the project's real acquisition loop. The Python runner
// builds it in a temporary module, without editing the backend go.mod or DB.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
	"github.com/simonvetter/modbus"
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
	for _, alias := range []string{rtu0Alias, envOr("ADR0014_RTU1", "/tmp/modbus-rtu1")} {
		channel := acquisition.Channel{ID: 1, Name: "simulator", Port: alias,
			BaudRate: 9600, DataBits: 8, Parity: "N", StopBits: 1, TimeoutMS: 150, Enabled: 1}
		session, err := acquisition.NewModbusSessionFactory()(channel)
		must(err)
		must(session.Open())
		store := acquisition.NewCurrentStateStore()
		devices := []acquisition.Device{{ID: 1, Name: "feeder-1", ChannelID: 1, SlaveID: 1,
			DeviceType: acquisition.DeviceTypeFeedProtector, FailureThreshold: 3, Enabled: 1,
			RegisterBlocks: []acquisition.RegisterBlock{
				{ID: 101, Name: "holding", FunctionCode: acquisition.FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 2, SortOrder: 0},
				{ID: 102, Name: "input", FunctionCode: acquisition.FunctionCodeReadInputRegisters, StartAddress: 0, Quantity: 1, SortOrder: 1},
			}}}
		if alias == rtu0Alias {
			devices = append(devices, acquisition.Device{ID: 2, Name: "feeder-2", ChannelID: 1, SlaveID: 2,
				DeviceType: acquisition.DeviceTypeFeedProtector, FailureThreshold: 3, Enabled: 1,
				RegisterBlocks: []acquisition.RegisterBlock{
					{ID: 201, Name: "holding", FunctionCode: acquisition.FunctionCodeReadHoldingRegisters, StartAddress: 0, Quantity: 2, SortOrder: 0},
					{ID: 202, Name: "input", FunctionCode: acquisition.FunctionCodeReadInputRegisters, StartAddress: 0, Quantity: 1, SortOrder: 1},
				}})
		}
		must(acquisition.PollChannelOnce(ctx, channel, devices, session, store))
		inputFirst := uint16(42)
		checkState(store, 1, 3000, inputFirst)
		if len(devices) == 2 {
			checkState(store, 2, 3100, 43)
			devices[0].SlaveID = 99 // Unknown slave: real timeout, other slave still collects.
			for i := 0; i < 3; i++ {
				must(acquisition.PollChannelOnce(ctx, channel, devices, session, store))
			}
			state, _ := store.Get(1)
			if state.Status != acquisition.StatusOffline || state.ConsecutiveFailures != 3 || state.LastError == "" {
				panic(fmt.Sprintf("expected offline: %+v", state))
			}
			checkState(store, 2, 3100, 43)
			devices[0].SlaveID = 1
			must(acquisition.PollChannelOnce(ctx, channel, devices, session, store))
			checkState(store, 1, 3000, inputFirst)
			fmt.Println("PASS real Go timeout -> OFFLINE, other slave ONLINE, recovery -> ONLINE")
		}
		must(session.Close())
	}
	if os.Getenv("GO_SMOKE_RT_ONLY") == "1" {
		fmt.Fprintln(os.Stdout, "ALL GO RTU SMOKE CHECKS PASSED")
		return
	}
	for _, url := range []string{"tcp://127.0.0.1:1502", "udp://127.0.0.1:1600", "rtuoverudp://127.0.0.1:1700"} {
		client, err := modbus.NewClient(&modbus.ClientConfiguration{URL: url, Timeout: time.Second,
			Logger: log.New(io.Discard, "", 0)})
		must(err)
		must(client.Open())
		for _, id := range []uint8{1, 2} {
			must(client.SetUnitId(id))
			values, err := client.ReadRegisters(0, 7, modbus.HOLDING_REGISTER)
			must(err)
			want := uint16(3000)
			if id == 2 {
				want = 3100
			}
			if len(values) != 7 || values[0] != want || values[1] != 125 || values[2] != 0 ||
				values[3] != 1234 || values[4] != 5000 || values[5] != 980 || values[6] != 0 {
				panic(fmt.Sprintf("bad network values: %v", values))
			}
			fmt.Printf("PASS Go library %s unit=%d FC03 address=0 count=7 registers=%v\n", url, id, values)
		}
		must(client.Close())
	}
	fmt.Fprintln(os.Stdout, "ALL GO SMOKE CHECKS PASSED")
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
