import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";

const repoRoot = fileURLToPath(new URL("../../", import.meta.url));
const apiDir = path.join(repoRoot, "edge-collector-api");
const simulatorDir = path.join(repoRoot, "modbus-simulator");
const apiPort = Number(process.env.ZNCK_I_E2E_API_PORT ?? 8111);
const apiBase = `http://127.0.0.1:${apiPort}`;
const children = [];

function startProcess(command, args, cwd, environment = {}) {
  const child = spawn(command, args, {
    cwd,
    env: { ...process.env, ...environment },
    detached: true,
    stdio: ["ignore", "pipe", "pipe"],
  });
  child.output = "";
  const collect = (chunk) => {
    child.output = `${child.output}${chunk}`.slice(-32000);
  };
  child.stdout.on("data", collect);
  child.stderr.on("data", collect);
  children.push(child);
  return child;
}

function signalProcessGroup(child, signal) {
  if (!child.pid) return;
  try {
    process.kill(-child.pid, signal);
  } catch {
    // The process group may already have exited.
  }
}

async function stopProcess(child) {
  if (child.exitCode !== null || child.signalCode !== null) {
    signalProcessGroup(child, "SIGTERM");
    return;
  }
  const closed = new Promise((resolve) => child.once("close", resolve));
  signalProcessGroup(child, "SIGTERM");
  await Promise.race([closed, delay(2000)]);
  if (child.exitCode === null) {
    const killed = new Promise((resolve) => child.once("close", resolve));
    signalProcessGroup(child, "SIGKILL");
    await Promise.race([killed, delay(1000)]);
  }
}

async function waitForOutput(child, expected, timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (child.output.includes(expected)) return;
    if (child.exitCode !== null) {
      throw new Error(`process exited before ${expected}:\n${child.output}`);
    }
    await delay(100);
  }
  throw new Error(`process did not emit ${expected}:\n${child.output}`);
}

async function waitForHTTP(url, child, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.ok) return;
    } catch {
      // The process is still starting.
    }
    if (child.exitCode !== null) {
      throw new Error(`process exited while waiting for ${url}:\n${child.output}`);
    }
    await delay(150);
  }
  throw new Error(`endpoint did not become ready: ${url}\n${child.output}`);
}

async function apiJSON(pathname, token, options = {}) {
  const response = await fetch(`${apiBase}${pathname}`, {
    ...options,
    headers: {
      ...(options.body !== undefined ? { "Content-Type": "application/json" } : {}),
      ...(token ? { Authorization: token } : {}),
      ...(options.headers ?? {}),
    },
    body: options.body !== undefined && typeof options.body !== "string"
      ? JSON.stringify(options.body)
      : options.body,
  });
  const payload = await response.json();
  assert.equal(response.status, 200, `${options.method ?? "GET"} ${pathname}: ${JSON.stringify(payload)}`);
  assert.equal(payload.code, 200, `${options.method ?? "GET"} ${pathname}: ${JSON.stringify(payload)}`);
  return payload.data;
}

async function runCommand(command, args, cwd, environment) {
  const child = startProcess(command, args, cwd, environment);
  const code = await new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("close", resolve);
  });
  if (code !== 0) throw new Error(`${command} ${args.join(" ")} failed (${code}):\n${child.output}`);
}

async function waitForScriptState(token, deviceID, timeout = 30000) {
  const deadline = Date.now() + timeout;
  let lastStates = [];
  while (Date.now() < deadline) {
    lastStates = await apiJSON("/api/v1/acquisition/script-states", token);
    const state = lastStates.find((item) => item.deviceId === deviceID);
    if (
      state
      && state.lastSuccessAt
      && state.state?.fault_code === 7
      && state.events.length === 1
      && state.events[0].kind === "znck_i_raw"
      && state.events[0].payload?.index === 0
      && state.events[0].payload?.registers?.length === 17
    ) {
      return state;
    }
    await delay(200);
  }
  throw new Error(`script state did not reach ZNCK-I success: ${JSON.stringify(lastStates)}`);
}

function channelLogLines(simulator, channel) {
  return simulator.output
    .split("\n")
    .filter((line) => line.includes(`channel=${channel}`) && line.includes("result=OK"));
}

function assertZnckSequence(simulator, channel) {
  const lines = channelLogLines(simulator, channel);
  const staticIndex = lines.findIndex((line) => /function=03 address=8166 count=1/.test(line));
  const writeIndex = lines.findIndex((line) => /function=10 address=8120 count=1/.test(line));
  const detailIndex = lines.findIndex((line) => /function=03 address=8121 count=17/.test(line));
  assert.ok(staticIndex >= 0, `${channel}: static 8166 read missing\n${lines.join("\n")}`);
  assert.ok(writeIndex >= 0, `${channel}: FC16 8120 write missing\n${lines.join("\n")}`);
  assert.ok(detailIndex >= 0, `${channel}: detail 8121..8137 read missing\n${lines.join("\n")}`);
  assert.ok(staticIndex < writeIndex && writeIndex < detailIndex, `${channel}: request order invalid\n${lines.join("\n")}`);
  assert.equal(lines.filter((line) => /function=10 address=8120 count=1/.test(line)).length, 2, `${channel}: 7→0→7 did not trigger FC16 twice`);
  assert.equal(lines.filter((line) => /function=03 address=8121 count=17/.test(line)).length, 2, `${channel}: 7→0→7 did not trigger FC03 twice`);
}

const source = `def after_poll(ctx):
    code = ctx.raw_register(3, 8166)
    if code == None:
        return
    previous = ctx.state_get("fault_code")
    if code == 0:
        ctx.state_set("fault_code", 0)
        return
    if previous == code:
        return
    ctx.write_registers(8120, [0])
    ctx.delay(50)
    detail = ctx.read_holding(8121, 17)
    key = str(detail[0]) + ":" + str(detail[16]) + ":" + str(detail[10]) + ":" + str(detail[11]) + ":" + str(detail[12]) + ":" + str(detail[13]) + ":" + str(detail[14]) + ":" + str(detail[15])
    ctx.emit_event("znck_i_raw", key, {"index": 0, "registers": detail})
    ctx.state_set("fault_code", code)
`;

const tempRoot = await mkdtemp(path.join(os.tmpdir(), "edge-collector-znck-i-e2e-"));
const environment = {
  APP_ENV: "dev",
  APP_CONFIG_PROFILE: "sqlite",
  APP_DATABASE__URL: path.join(tempRoot, "e2e.db"),
  APP_FILE__STORAGE_ROOT: path.join(tempRoot, "uploads"),
  APP_HTTP__ADDRESS: `127.0.0.1:${apiPort}`,
  APP_JWT__SECRET: "edge-collector-znck-i-e2e-secret",
  APP_LOG__LEVEL: "warn",
  GOCACHE: path.join(tempRoot, "go-build"),
  UV_CACHE_DIR: process.env.UV_CACHE_DIR ?? "/tmp/edge-collector-uv-cache",
};

try {
  const simulator = startProcess(
    "uv",
    ["run", "modbus-simulator", "--config", "config/znck-i-runtime-e2e.yaml"],
    simulatorDir,
  );
  await waitForOutput(simulator, "Modbus Simulator Started");

  await runCommand("go", ["run", "./cmd/migrate", "up", "--kind", "all"], apiDir, environment);
  const api = startProcess("go", ["run", "./cmd/api"], apiDir, environment);
  await waitForHTTP(`${apiBase}/health`, api);

  const login = await apiJSON("/api/auth/login", "", {
    method: "POST",
    body: { username: "admin", password: "admin123" },
  });
  const token = login.tokenValue;
  const script = await apiJSON("/api/v1/acquisition/scripts", token, {
    method: "POST",
    body: { name: "ZNCK-I runtime E2E", description: "raw dynamic transaction", draftSource: source },
  });
  const validation = await apiJSON(`/api/v1/acquisition/scripts/${script.id}/validate`, token, { method: "POST" });
  assert.equal(validation.valid, true, JSON.stringify(validation));
  const version = await apiJSON(`/api/v1/acquisition/scripts/${script.id}/publish`, token, { method: "POST" });
  assert.equal(version.versionNo, 1);

  const channels = [];
  channels.push(await apiJSON("/api/v1/acquisition/channels", token, {
    method: "POST",
    body: {
      name: "ZNCK-I RTU E2E",
      protocol: "MODBUS_RTU",
      serialConfig: { port: "/tmp/modbus-adr0016-znck-rtu", baudRate: 9600, dataBits: 8, stopBits: 1, parity: "N" },
      timeoutMs: 1000,
      interRequestDelayMs: 5,
      enabled: 1,
    },
  }));
  channels.push(await apiJSON("/api/v1/acquisition/channels", token, {
    method: "POST",
    body: { name: "ZNCK-I TCP E2E", protocol: "MODBUS_TCP", timeoutMs: 1000, interRequestDelayMs: 5, enabled: 1 },
  }));

  const devices = [];
  devices.push(await apiJSON("/api/v1/acquisition/devices", token, {
    method: "POST",
    body: {
      name: "ZNCK-I RTU Device",
      deviceType: "FEED_PROTECTOR",
      channelId: channels[0].id,
      unitId: 1,
      pollIntervalMs: 200,
      failureThreshold: 2,
      enabled: 1,
      scriptId: script.id,
      registerBlocks: [{ name: "fault-code", functionCode: 3, startAddress: 8166, quantity: 1, sortOrder: 0 }],
    },
  }));
  devices.push(await apiJSON("/api/v1/acquisition/devices", token, {
    method: "POST",
    body: {
      name: "ZNCK-I TCP Device",
      deviceType: "FEED_PROTECTOR",
      channelId: channels[1].id,
      unitId: 1,
      networkEndpoint: { host: "127.0.0.1", port: 1516 },
      pollIntervalMs: 200,
      failureThreshold: 2,
      enabled: 1,
      scriptId: script.id,
      registerBlocks: [{ name: "fault-code", functionCode: 3, startAddress: 8166, quantity: 1, sortOrder: 0 }],
    },
  }));

  for (const device of devices) {
    const state = await waitForScriptState(token, device.id);
    assert.equal(state.versionNo, 1);
    const staticState = await apiJSON(`/api/v1/acquisition/states/${device.id}`, token);
    assert.equal(staticState.status, "ONLINE");
    assert.equal(staticState.registerBlocks[0].valid, true);
    assert.deepEqual(staticState.registerBlocks[0].values, [7]);
  }

  await delay(700);
  assertZnckSequence(simulator, "znck-i-rtu-e2e");
  assertZnckSequence(simulator, "znck-i-tcp-e2e");
  console.log("ZNCK-I RTU + TCP runtime E2E passed", JSON.stringify({ devices: devices.length, version: version.versionNo }));
} finally {
  for (const child of children.reverse()) await stopProcess(child);
  await rm(tempRoot, { recursive: true, force: true });
}
