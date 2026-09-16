import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { access, mkdtemp, rm } from "node:fs/promises";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const repoRoot = fileURLToPath(new URL("../../", import.meta.url));
const apiDir = path.join(repoRoot, "edge-collector-api");
const simulatorDir = path.join(repoRoot, "modbus-simulator");
const apiPort = 8109;
const webPort = 4181;
const apiBase = `http://127.0.0.1:${apiPort}`;
const webBase = `http://127.0.0.1:${webPort}`;

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
    child.output = `${child.output}${chunk}`.slice(-16000);
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

async function waitForOutput(child, text, timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (child.output.includes(text)) return;
    if (child.exitCode !== null) {
      throw new Error(`process exited before ${text}:\n${child.output}`);
    }
    await delay(100);
  }
  throw new Error(`process did not emit ${text}:\n${child.output}`);
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
    if (child.exitCode !== null) throw new Error(`process exited while waiting for ${url}:\n${child.output}`);
    await delay(150);
  }
  throw new Error(`endpoint did not become ready: ${url}\n${child.output}`);
}

async function waitForPort(port, timeout = 15000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const connected = await new Promise((resolve) => {
      const socket = net.createConnection({ host: "127.0.0.1", port });
      socket.once("connect", () => {
        socket.destroy();
        resolve(true);
      });
      socket.once("error", () => {
        socket.destroy();
        resolve(false);
      });
    });
    if (connected) return;
    await delay(100);
  }
  throw new Error(`TCP endpoint did not become ready: ${port}`);
}

async function runCommand(command, args, cwd, environment) {
  const child = startProcess(command, args, cwd, environment);
  const code = await new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("close", resolve);
  });
  if (code !== 0) throw new Error(`${command} ${args.join(" ")} failed (${code}):\n${child.output}`);
}

async function apiJSON(pathname, token, options = {}) {
  const headers = { ...(options.body ? { "Content-Type": "application/json" } : {}), ...(token ? { Authorization: token } : {}) };
  const response = await fetch(`${apiBase}${pathname}`, {
    ...options,
    headers: { ...headers, ...(options.headers ?? {}) },
    body: options.body && typeof options.body !== "string" ? JSON.stringify(options.body) : options.body,
  });
  const payload = await response.json();
  assert.equal(payload.code, 200, `${options.method ?? "GET"} ${pathname}: ${JSON.stringify(payload)}`);
  return payload.data;
}

async function waitForAcquisition(token, deviceCount, channelIDs) {
  const deadline = Date.now() + 45000;
  while (Date.now() < deadline) {
    const [states, channels] = await Promise.all([
      apiJSON("/api/v1/acquisition/states", token),
      apiJSON("/api/v1/acquisition/channel-state", token),
    ]);
    const selectedChannels = channels.filter((channel) => channelIDs.includes(channel.channelId));
    if (
      states.length === deviceCount &&
      states.every((state) => state.status === "ONLINE" && state.registerBlocks.length === 2 && state.registerBlocks.every((block) => block.valid)) &&
      selectedChannels.length === channelIDs.length &&
      selectedChannels.every((channel) => channel.status === "ONLINE")
    ) {
      return { states, channels };
    }
    await delay(250);
  }
  throw new Error("四协议设备未在期限内全部 ONLINE");
}

function devicePayload(device, port = device.networkEndpoint?.port) {
  return {
    name: device.name,
    deviceType: device.deviceType,
    channelId: device.channelId,
    unitId: device.unitId,
    ...(device.networkEndpoint ? { networkEndpoint: { host: device.networkEndpoint.host, port } } : {}),
    pollIntervalMs: device.pollIntervalMs,
    failureThreshold: device.failureThreshold,
    enabled: device.enabled,
    registerBlocks: device.registerBlocks.map((block) => ({
      id: block.id,
      name: block.name,
      functionCode: block.functionCode,
      startAddress: block.startAddress,
      quantity: block.quantity,
      sortOrder: block.sortOrder,
    })),
  };
}

const tempRoot = await mkdtemp(path.join(os.tmpdir(), "edge-collector-adr0015-e2e-"));
const environment = {
  APP_ENV: "dev",
  APP_CONFIG_PROFILE: "sqlite",
  APP_DATABASE__URL: path.join(tempRoot, "e2e.db"),
  APP_FILE__STORAGE_ROOT: path.join(tempRoot, "uploads"),
  APP_HTTP__ADDRESS: `127.0.0.1:${apiPort}`,
  APP_JWT__SECRET: "edge-collector-adr0015-e2e-secret",
  APP_LOG__LEVEL: "warn",
  GOCACHE: path.join(repoRoot, ".task", "go-build"),
};

try {
  const simulator = startProcess("uv", ["run", "modbus-simulator", "--config", "config/adr0015-e2e.yaml"], simulatorDir);
  await waitForOutput(simulator, "Modbus Simulator Started");
  await waitForPort(2502);
  await access("/tmp/modbus-adr0015-rtu");

  await runCommand("go", ["run", "./cmd/migrate", "up", "--kind", "all"], apiDir, environment);
  const api = startProcess("go", ["run", "./cmd/api"], apiDir, environment);
  await waitForHTTP(`${apiBase}/health`, api);
  const web = startProcess("node", ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(webPort)], path.join(repoRoot, "react-admin"), {
    VITE_API_BASE_URL: apiBase,
  });
  await waitForHTTP(`${webBase}/`, web);

  const login = await apiJSON("/api/auth/login", "", { method: "POST", body: { username: "admin", password: "admin123" } });
  const token = login.tokenValue;
  const channels = [];
  channels.push(await apiJSON("/api/v1/acquisition/channels", token, {
    method: "POST",
    body: {
      name: "E2E RTU",
      protocol: "MODBUS_RTU",
      serialConfig: { port: "/tmp/modbus-adr0015-rtu", baudRate: 9600, dataBits: 8, stopBits: 1, parity: "N" },
      timeoutMs: 500,
      interRequestDelayMs: 5,
      enabled: 1,
    },
  }));
  for (const [name, protocol] of [["E2E TCP", "MODBUS_TCP"], ["E2E MBAP UDP", "MODBUS_UDP"], ["E2E RTU over UDP", "MODBUS_RTU_OVER_UDP"]]) {
    channels.push(await apiJSON("/api/v1/acquisition/channels", token, {
      method: "POST",
      body: { name, protocol, timeoutMs: 500, interRequestDelayMs: 5, enabled: 1 },
    }));
  }

  const endpointPorts = {
    MODBUS_TCP: [2502, 2503, 2504],
    MODBUS_UDP: [2600, 2601, 2602],
    MODBUS_RTU_OVER_UDP: [2700, 2701, 2702],
  };
  const devices = [];
  const rtuChannel = channels[0];
  for (const [index, unitId] of [1, 2].entries()) {
    devices.push(await apiJSON("/api/v1/acquisition/devices", token, {
      method: "POST",
      body: {
        name: `E2E RTU Unit ${unitId}`,
        deviceType: "FEED_PROTECTOR",
        channelId: rtuChannel.id,
        unitId,
        pollIntervalMs: 200,
        failureThreshold: 2,
        enabled: 1,
        registerBlocks: [
          { name: "holding", functionCode: 3, startAddress: 0, quantity: 2, sortOrder: index * 2 },
          { name: "input", functionCode: 4, startAddress: 0, quantity: 1, sortOrder: index * 2 + 1 },
        ],
      },
    }));
  }
  for (const channel of channels.slice(1)) {
    for (const [index, port] of endpointPorts[channel.protocol].slice(0, 2).entries()) {
      devices.push(await apiJSON("/api/v1/acquisition/devices", token, {
        method: "POST",
        body: {
          name: `${channel.name} endpoint ${index + 1}`,
          deviceType: "FEED_PROTECTOR",
          channelId: channel.id,
          unitId: 1,
          networkEndpoint: { host: "127.0.0.1", port },
          pollIntervalMs: 200,
          failureThreshold: 2,
          enabled: 1,
          registerBlocks: [
            { name: "holding", functionCode: 3, startAddress: 0, quantity: 2, sortOrder: 0 },
            { name: "input", functionCode: 4, startAddress: 0, quantity: 1, sortOrder: 1 },
          ],
        },
      }));
    }
  }

  const networkPage = await apiJSON("/api/v1/acquisition/devices?page=1&pageSize=20", token);
  for (const channel of channels.slice(1)) {
    const channelDevices = networkPage.records.filter((device) => device.channelId === channel.id);
    assert.equal(new Set(channelDevices.map((device) => `${device.networkEndpoint.host}:${device.networkEndpoint.port}`)).size, 2);
    assert.ok(channelDevices.every((device) => device.unitId === 1));
  }
  const channelIDs = channels.map((channel) => channel.id);
  const initial = await waitForAcquisition(token, devices.length, channelIDs);
  assert.equal(initial.states.length, 8);
  assert.ok(initial.states.every((state) => state.registerBlocks.length === 2));

  const browser = await chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}),
  });
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
    await page.addInitScript((value) => localStorage.setItem("react-admin-auth", JSON.stringify({ token: value, user: null })), token);
    await page.goto(`${webBase}/acquisition/device`, { waitUntil: "networkidle" });
    await page.getByRole("heading", { name: "设备管理", exact: true }).waitFor();
    await page.getByText("127.0.0.1:2502", { exact: true }).waitFor();
    await page.getByText("127.0.0.1:2503", { exact: true }).waitFor();
    await page.getByText("127.0.0.1:2600", { exact: true }).waitFor();
    await page.getByText("127.0.0.1:2701", { exact: true }).waitFor();

    await page.getByRole("button", { name: "新建设备", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("heading", { name: "新建设备", exact: true }).waitFor();
    await dialog.locator('select[name="channelId"]').selectOption(String(channels[3].id));
    const unitID = dialog.locator('input[name="unitId"]');
    assert.equal(await unitID.getAttribute("min"), "1");
    assert.equal(await unitID.getAttribute("max"), "247");
    await dialog.locator('input[name="name"]').fill("E2E 非法 Unit ID");
    await dialog.locator('input[name="registerBlocks.0.name"]').fill("holding");
    await unitID.fill("248");
    await dialog.getByRole("button", { name: "保存", exact: true }).click();
    await dialog.getByText("Modbus RTU over UDP Unit ID 范围为 1～247", { exact: true }).waitFor();
    await dialog.getByRole("button", { name: "取消", exact: true }).click();

    await page.goto(`${webBase}/acquisition/realtime`, { waitUntil: "networkidle" });
    await page.getByTestId("summary-online").waitFor();
    assert.equal(await page.getByTestId("summary-online").innerText(), "8");
    for (const channel of channels) {
      await page.getByTestId(`channel-runtime-${channel.id}`).getByText("在线", { exact: true }).waitFor();
    }

    for (const channel of channels.slice(1)) {
      const first = devices.find((device) => device.channelId === channel.id);
      const nextPort = endpointPorts[channel.protocol][2];
      await apiJSON(`/api/v1/acquisition/devices/${first.id}`, token, { method: "PUT", body: devicePayload(first, nextPort) });
      const deadline = Date.now() + 15000;
      while (Date.now() < deadline) {
        const state = (await apiJSON(`/api/v1/acquisition/states/${first.id}`, token));
        if (state.status === "ONLINE" && state.networkEndpoint.port === nextPort && state.registerBlocks.every((block) => block.valid)) break;
        await delay(200);
      }
      const updated = await apiJSON(`/api/v1/acquisition/states/${first.id}`, token);
      assert.equal(updated.status, "ONLINE");
      assert.equal(updated.networkEndpoint.port, nextPort);
    }
    await page.goto(`${webBase}/acquisition/device`, { waitUntil: "networkidle" });
    await page.getByText("127.0.0.1:2504", { exact: true }).waitFor();
    await page.getByText("127.0.0.1:2602", { exact: true }).waitFor();
    await page.getByText("127.0.0.1:2702", { exact: true }).waitFor();
    console.log("acquisition DB → cmd/api → browser E2E passed", JSON.stringify({ devices: devices.length, channels: channels.length }));
  } finally {
    await browser.close();
  }
} finally {
  for (const child of children.reverse()) await stopProcess(child);
  await rm(tempRoot, { recursive: true, force: true });
}
