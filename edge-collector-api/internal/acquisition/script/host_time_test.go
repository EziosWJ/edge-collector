package script

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestHostTimeIsStablePerInvocationAndUsesConfiguredZone(t *testing.T) {
	zone := time.FixedZone("SITE", 8*60*60)
	base := time.Date(2026, 9, 17, 12, 34, 56, 0, zone)
	calls := 0
	runtime := NewRuntime(Options{HostTime: func() time.Time {
		calls++
		return base.Add(time.Duration(calls-1) * time.Second)
	}})
	compiled, err := runtime.Compile(testVersion(`def after_poll(ctx):
    ctx.state_set("first", ctx.host_time())
    ctx.state_set("second", ctx.host_time())
`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("HostTime calls = %d, want 1 per invocation", calls)
	}
	want := map[string]any{
		"year":               int64(2026),
		"month":              int64(9),
		"day":                int64(17),
		"hour":               int64(12),
		"minute":             int64(34),
		"second":             int64(56),
		"unix":               base.Unix(),
		"timezone":           "SITE",
		"utc_offset_seconds": int64(8 * 60 * 60),
	}
	if !reflect.DeepEqual(result.State["first"], want) || !reflect.DeepEqual(result.State["second"], want) {
		t.Fatalf("host time state = %#v / %#v, want %#v", result.State["first"], result.State["second"], want)
	}
}

func TestHostTimeDriftScriptWritesOnlyWhenNeeded(t *testing.T) {
	zone := time.FixedZone("SITE", 8*60*60)
	hostAt := time.Date(2026, 9, 17, 12, 34, 56, 0, zone)
	runtime := NewRuntime(Options{HostTime: func() time.Time { return hostAt }})
	compiled, err := runtime.Compile(testVersion(`MAX_DRIFT_SECONDS = 2

def seconds_of_day(hour, minute, second):
    return hour * 3600 + minute * 60 + second

def after_poll(ctx):
    device_hour = ctx.raw_register(3, 100)
    device_minute = ctx.raw_register(3, 101)
    device_second = ctx.raw_register(3, 102)
    if device_hour == None or device_minute == None or device_second == None:
        return

    now = ctx.host_time()
    if device_hour > 23 or device_minute > 59 or device_second > 59:
        ctx.write_registers(100, [now["hour"], now["minute"], now["second"]])
        return

    device_time = seconds_of_day(device_hour, device_minute, device_second)
    host_time = seconds_of_day(now["hour"], now["minute"], now["second"])
    diff = abs(device_time - host_time)
    if diff > 43200:
        diff = 86400 - diff
    if diff <= MAX_DRIFT_SECONDS:
        return

    ctx.write_registers(100, [now["hour"], now["minute"], now["second"]])
`))
	if err != nil {
		t.Fatal(err)
	}

	near := &testHost{raw: map[[2]int]uint16{{3, 100}: 12, {3, 101}: 34, {3, 102}: 55}}
	if _, err := runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 10}, near); err != nil {
		t.Fatal(err)
	}
	if len(near.writes) != 0 {
		t.Fatalf("near clock writes = %#v, want none", near.writes)
	}

	far := &testHost{raw: map[[2]int]uint16{{3, 100}: 12, {3, 101}: 34, {3, 102}: 40}}
	if _, err := runtime.Invoke(context.Background(), compiled, Invocation{DeviceID: 11}, far); err != nil {
		t.Fatal(err)
	}
	if len(far.writes) != 1 || far.writes[0].address != 100 || !reflect.DeepEqual(far.writes[0].values, []uint16{12, 34, 56}) {
		t.Fatalf("far clock writes = %#v, want one 12:34:56 write", far.writes)
	}
}
