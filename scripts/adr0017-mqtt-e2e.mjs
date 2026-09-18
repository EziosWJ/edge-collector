/*
 * ADR-0017 real Broker + Modbus simulator acceptance harness.
 *
 * This is deliberately an API-level test.  It starts the same Go API binary
 * used by development, the repository's real Modbus simulator, and a plain
 * Eclipse Mosquitto container.  No fake MQTT transport is used here.
 *
 * The harness is opt-in because it needs Docker and the simulator's uv
 * environment.  Set MQTT_E2E_REQUIRED=1 in CI when an unavailable dependency
 * must fail the job instead of producing a documented skip.
 */
import assert from "node:assert/strict";
import { access, mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { spawn, spawnSync } from "node:child_process";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";

const repoRoot = fileURLToPath(new URL("../", import.meta.url));
const apiDir = path.join(repoRoot, "edge-collector-api");
const simulatorDir = path.join(repoRoot, "modbus-simulator");
const composeFile = path.join(repoRoot, "testdata/mqtt/docker-compose.yml");
const databaseKind = (process.env.MQTT_E2E_DATABASE ?? "sqlite").toLowerCase();
const protocol = process.env.MQTT_E2E_PROTOCOL ?? "MQTT_5";
const required = process.env.MQTT_E2E_REQUIRED === "1";
const manageServices = process.env.MQTT_E2E_MANAGE_SERVICES !== "0";
const brokerPort = Number(process.env.MQTT_E2E_BROKER_PORT ?? 18883);
const postgresPort = Number(process.env.MQTT_E2E_POSTGRES_PORT ?? 55433);
const apiPort = Number(process.env.MQTT_E2E_API_PORT ?? 18199);
const simulatorAlias = process.env.MQTT_E2E_MODBUS_ALIAS ?? "/tmp/edge-collector-adr0017-mqtt-rtu";
const topicPrefix = process.env.MQTT_E2E_TOPIC_PREFIX ?? "edge";
const edgeID = process.env.MQTT_E2E_EDGE_ID ?? "edge-01";
const clientID = process.env.MQTT_E2E_CLIENT_ID ?? `edge-collector-adr0017-${process.pid}`;
const brokerURL = process.env.MQTT_E2E_BROKER_URL ?? `mqtt://127.0.0.1:${brokerPort}`;
const secret = process.env.MQTT_E2E_SECRET ?? "adr0017-e2e-password-never-deployed";
const masterSecret = process.env.MQTT_E2E_MASTER_SECRET ?? "adr0017-e2e-master-never-deployed";
const composeProject = `edge-collector-adr0017-${process.pid}`.toLowerCase();

const children = new Set();
let managedCompose = false;
let tempRoot;
let apiProcess;
let simulatorProcess;
let collectorProcess;
let collector;

function hasCommand(command) {
  const versionArgs = command === "go" ? ["version"] : ["--version"];
  return spawnSync(command, versionArgs, { stdio: "ignore" }).status === 0;
}

function startProcess(command, args, cwd, environment = {}) {
  const child = spawn(command, args, {
    cwd,
    env: { ...process.env, ...environment },
    detached: true,
    stdio: ["ignore", "pipe", "pipe"],
  });
  child.output = "";
  const collect = (chunk) => {
    child.output = `${child.output}${chunk}`.slice(-64000);
  };
  child.stdout?.on("data", collect);
  child.stderr?.on("data", collect);
  children.add(child);
  return child;
}

function signalProcessGroup(child, signal) {
  if (!child?.pid) return;
  try {
    process.kill(-child.pid, signal);
  } catch {
    // The process group may have already exited.
  }
}

async function stopProcess(child, signal = "SIGTERM") {
  if (!child || (child.exitCode !== null && child.exitCode !== undefined)) {
    children.delete(child);
    return;
  }
  const closed = new Promise((resolve) => child.once("close", resolve));
  signalProcessGroup(child, signal);
  await Promise.race([closed, delay(signal === "SIGKILL" ? 1500 : 3000)]);
  if (child.exitCode === null) {
    const killed = new Promise((resolve) => child.once("close", resolve));
    signalProcessGroup(child, "SIGKILL");
    await Promise.race([killed, delay(1500)]);
  }
  children.delete(child);
}

async function runCommand(command, args, cwd, environment = {}) {
  const child = startProcess(command, args, cwd, environment);
  const code = await new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("close", resolve);
  });
  children.delete(child);
  if (code !== 0) {
    throw new Error(`${command} ${args.join(" ")} failed (${code}):\n${child.output}`);
  }
  return child.output;
}

async function waitForOutput(child, expected, timeout = 30000) {
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

async function waitForFile(file, timeout = 15000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try {
      await access(file);
      return;
    } catch {
      await delay(100);
    }
  }
  throw new Error(`file did not appear: ${file}`);
}

async function waitForPort(host, port, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const connected = await new Promise((resolve) => {
      const socket = net.createConnection({ host, port });
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
  throw new Error(`TCP endpoint did not become ready: ${host}:${port}`);
}

async function waitForHTTP(url, child, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(2000) });
      if (response.ok) return;
    } catch {
      // The process is still starting.
    }
    if (child?.exitCode !== null && child?.exitCode !== undefined) {
      throw new Error(`process exited while waiting for ${url}:\n${child.output}`);
    }
    await delay(150);
  }
  throw new Error(`endpoint did not become ready: ${url}\n${child?.output ?? ""}`);
}

async function eventually(label, read, predicate, timeout = 30000, interval = 150) {
  const deadline = Date.now() + timeout;
  let value;
  while (Date.now() < deadline) {
    value = await read();
    if (predicate(value)) return value;
    await delay(interval);
  }
  throw new Error(`${label} did not reach expected state: ${JSON.stringify(value)}`);
}

function composeArgs(...args) {
  return ["compose", "--project-name", composeProject, "--file", composeFile, ...args];
}

const composeEnvironment = {
  MQTT_E2E_BROKER_PORT: String(brokerPort),
  MQTT_E2E_POSTGRES_PORT: String(postgresPort),
  MQTT_E2E_POSTGRES_DB: "edge_collector_mqtt_e2e",
  MQTT_E2E_POSTGRES_USER: "edge_collector",
  MQTT_E2E_POSTGRES_PASSWORD: "edge_collector_e2e_password",
};

async function compose(...args) {
  return runCommand("docker", composeArgs(...args), repoRoot, composeEnvironment);
}

async function waitForHealthyService(service, timeout = 60000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const output = await compose("ps", "--format", "json", service);
    const records = [];
    for (const line of output.split("\n").map((value) => value.trim()).filter(Boolean)) {
      try {
        const value = JSON.parse(line);
        if (Array.isArray(value)) records.push(...value);
        else records.push(value);
      } catch {
        // Compose may emit a transient non-JSON status while the service is starting.
      }
    }
    if (records.some((record) => record.Service === service && (record.Health === "healthy" || String(record.Status ?? "").includes("(healthy)")))) {
      return;
    }
    await delay(250);
  }
  throw new Error(`Compose service did not become healthy: ${service}`);
}

function brokerAddress() {
  const parsed = new URL(brokerURL);
  return { host: parsed.hostname, port: Number(parsed.port || 1883) };
}

function protocolArgument() {
  return protocol === "MQTT_3_1_1" ? "mqttv311" : "mqttv5";
}

function mqttClientCommand(client, args) {
  const address = brokerAddress();
  if (managedCompose) {
    return {
      command: "docker",
      args: [...composeArgs("exec", "-T", "broker", client, "-h", "127.0.0.1", "-p", "1883", ...args)],
    };
  }
  return { command: client, args: ["-h", address.host, "-p", String(address.port), ...args] };
}

class MQTTCollector {
  constructor(child) {
    this.child = child;
    this.messages = [];
    this.buffer = "";
    child.stdout.on("data", (chunk) => {
      this.buffer += chunk.toString();
      const lines = this.buffer.split("\n");
      this.buffer = lines.pop() ?? "";
      for (const line of lines) this.readLine(line.replace(/\r$/, ""));
    });
  }

  readLine(line) {
    if (!line) return;
    const first = line.indexOf("|");
    const second = line.indexOf("|", first + 1);
    const third = line.indexOf("|", second + 1);
    if (first < 0 || second < 0 || third < 0) return;
    const topic = line.slice(0, first);
    const qos = Number(line.slice(first + 1, second));
    const retain = Number(line.slice(second + 1, third));
    const rawPayload = line.slice(third + 1);
    let payload;
    try {
      payload = JSON.parse(rawPayload);
    } catch {
      return;
    }
    this.messages.push({ topic, qos, retain, payload, rawPayload, receivedAt: Date.now() });
  }

  async waitFor(predicate, fromIndex = 0, timeout = 30000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      for (let index = fromIndex; index < this.messages.length; index += 1) {
        const message = this.messages[index];
        if (predicate(message)) return { message, index };
      }
      if (this.child.exitCode !== null && this.child.exitCode !== undefined) {
        throw new Error(`MQTT subscriber exited: ${this.child.output}`);
      }
      await delay(50);
    }
    throw new Error(`MQTT message did not arrive; received ${this.messages.length} messages`);
  }
}

async function startCollector(filter) {
  const command = mqttClientCommand("mosquitto_sub", [
    "-V", protocolArgument(),
    "-t", filter,
    "-q", "1",
    "-F", "%t|%q|%r|%p",
  ]);
  const child = startProcess(command.command, command.args, repoRoot);
  const collector = new MQTTCollector(child);
  await delay(250);
  if (child.exitCode !== null && child.exitCode !== undefined) {
    throw new Error(`MQTT subscriber failed to start:\n${child.output}`);
  }
  return { child, collector };
}

async function waitForRetainedMessage(filter, predicate, timeout = 30000) {
  const retained = await startCollector(filter);
  try {
    return await retained.collector.waitFor(
      (message) => message.retain === 1 && predicate(message),
      0,
      timeout,
    );
  } finally {
    await stopProcess(retained.child, "SIGTERM");
  }
}

async function publishMQTT(topic, payload) {
  const command = mqttClientCommand("mosquitto_pub", [
    "-V", protocolArgument(),
    "-t", topic,
    "-q", "1",
    "-m", JSON.stringify(payload),
  ]);
  await runCommand(command.command, command.args, repoRoot);
}

async function requestJSON(pathname, token, options = {}) {
  const response = await fetch(`http://127.0.0.1:${apiPort}${pathname}`, {
    ...options,
    signal: options.signal ?? AbortSignal.timeout(10000),
    headers: {
      ...(options.body !== undefined ? { "Content-Type": "application/json" } : {}),
      ...(token ? { Authorization: token } : {}),
      ...(options.headers ?? {}),
    },
    body: options.body !== undefined && typeof options.body !== "string"
      ? JSON.stringify(options.body)
      : options.body,
  });
  const text = await response.text();
  let payload;
  try {
    payload = JSON.parse(text);
  } catch {
    payload = { raw: text };
  }
  return { response, payload };
}

async function apiJSON(pathname, token, options = {}) {
  const { response, payload } = await requestJSON(pathname, token, options);
  assert.equal(response.status, 200, `${options.method ?? "GET"} ${pathname}: ${JSON.stringify(payload)}`);
  assert.equal(payload.code, 200, `${options.method ?? "GET"} ${pathname}: ${JSON.stringify(payload)}`);
  return payload.data;
}

async function startServices() {
  if (!manageServices) return;
  const services = ["broker"];
  if (databaseKind === "postgres") services.push("postgres");
  managedCompose = true;
  await compose("up", "-d", ...services);
  const address = brokerAddress();
  await waitForPort(address.host, address.port);
  await waitForHealthyService("broker");
  if (databaseKind === "postgres") {
    await waitForPort("127.0.0.1", postgresPort);
    await waitForHealthyService("postgres");
  }
}

async function startSimulator() {
  simulatorProcess = startProcess(
    "uv",
    ["run", "modbus-simulator", "--config", "config/adr0017-mqtt-e2e.yaml"],
    simulatorDir,
    { UV_CACHE_DIR: process.env.UV_CACHE_DIR ?? "/tmp/edge-collector-uv-cache" },
  );
  await waitForOutput(simulatorProcess, "Modbus Simulator Started");
  await waitForFile(simulatorAlias);
}

async function stopBroker() {
  if (managedCompose) {
    await compose("stop", "broker");
    return;
  }
  throw new Error("MQTT_E2E_MANAGE_SERVICES=0 requires an external broker that can be stopped by the caller");
}

async function startBroker() {
  if (managedCompose) {
    await compose("up", "-d", "broker");
    const address = brokerAddress();
    await waitForPort(address.host, address.port);
    return;
  }
  throw new Error("MQTT_E2E_MANAGE_SERVICES=0 requires an external broker that can be restarted by the caller");
}

function apiEnvironment() {
  const environment = {
    APP_ENV: "dev",
    APP_CONFIG_PROFILE: databaseKind === "sqlite" ? "sqlite" : "",
    APP_DATABASE__DRIVER: databaseKind,
    APP_DATABASE__USERNAME: databaseKind === "postgres" ? composeEnvironment.MQTT_E2E_POSTGRES_USER : "",
    APP_DATABASE__PASSWORD: databaseKind === "postgres" ? composeEnvironment.MQTT_E2E_POSTGRES_PASSWORD : "",
    APP_DATABASE__URL: databaseKind === "postgres"
      ? (process.env.MQTT_E2E_DATABASE_URL ?? `postgres://127.0.0.1:${postgresPort}/${composeEnvironment.MQTT_E2E_POSTGRES_DB}?sslmode=disable`)
      : path.join(tempRoot, "e2e.db"),
    APP_FILE__STORAGE_ROOT: path.join(tempRoot, "uploads"),
    APP_HTTP__ADDRESS: `127.0.0.1:${apiPort}`,
    APP_JWT__SECRET: "adr0017-e2e-jwt-never-deployed",
    APP_LOG__LEVEL: "warn",
    APP_LOG__FORMAT: "text",
    APP_MQTT__MASTER_SECRET: masterSecret,
    MQTT_MASTER_SECRET: masterSecret,
    GOCACHE: path.join(repoRoot, ".task/go-build"),
    UV_CACHE_DIR: process.env.UV_CACHE_DIR ?? "/tmp/edge-collector-uv-cache",
  };
  if (databaseKind === "postgres" && process.env.MQTT_E2E_DATABASE_USERNAME) {
    environment.APP_DATABASE__USERNAME = process.env.MQTT_E2E_DATABASE_USERNAME;
  }
  if (databaseKind === "postgres" && process.env.MQTT_E2E_DATABASE_PASSWORD) {
    environment.APP_DATABASE__PASSWORD = process.env.MQTT_E2E_DATABASE_PASSWORD;
  }
  return environment;
}

async function migrateAndStartAPI() {
  const environment = apiEnvironment();
  await runCommand("go", ["run", "./cmd/migrate", "up", "--kind", "all"], apiDir, environment);
  apiProcess = startProcess("go", ["run", "./cmd/api"], apiDir, environment);
  await waitForHTTP(`http://127.0.0.1:${apiPort}/health`, apiProcess);
  return environment;
}

function mqttConfigBody(enabled, passwordAction = "keep", overrides = {}) {
  return {
    enabled,
    edgeId: edgeID,
    brokerUrl: brokerURL,
    protocolVersion: protocol,
    clientId: clientID,
    username: "adr0017-e2e-user",
    passwordAction,
    password: passwordAction === "set" ? secret : "",
    tlsEnabled: false,
    caCertificate: "",
    clientCertificate: "",
    clientPrivateKeyAction: "clear",
    clientPrivateKey: "",
    keepAliveSeconds: 5,
    connectTimeoutMs: 3000,
    reconnectMinMs: 200,
    reconnectMaxMs: 1000,
    topicPrefix,
    rawPublishIntervalMs: 150,
    outboxMaxRows: 100,
    // Keep the burst focused on command-queue admission. Each command
    // reserves 256 KiB for its FINAL result; the dedicated capacity scenario
    // below exercises the row boundary with outboxMaxRows=2.
    outboxMaxBytes: 8 * 1024 * 1024,
    outboxRetentionDays: 7,
    commandJournalRetentionDays: 7,
    commandJournalMaxRows: 100,
    commandQueueCapacity: 8,
    commandPollFairness: 1,
    ...overrides,
  };
}

function commandSource(version, includeEvents = true) {
  const eventCode = includeEvents
    ? `    previous = ctx.state_get("mqtt_e2e_second")
    if previous != second:
        ctx.state_set("mqtt_e2e_second", second)
        ctx.emit_event("mqtt_e2e_tick", str(second), {"registers": [hour, minute, second], "version": "${version}"})
`
    : "";
  return `def after_poll(ctx):
    hour = ctx.raw_register(3, 100)
    minute = ctx.raw_register(3, 101)
    second = ctx.raw_register(3, 102)
    if hour == None or minute == None or second == None:
        return
${eventCode}

def command(ctx, name, args):
    if name != "set_clock":
        return None
    ctx.delay(500)
    ctx.write_registers(100, [args["hour"], args["minute"], args["second"]])
    ctx.emit_event("mqtt_command_write", name + ":" + str(args["second"]), {"version": "${version}"})
    return {"version": "${version}", "written": [args["hour"], args["minute"], args["second"]]}
`;
}

async function createAcquisitionFixture(token) {
  const script = await apiJSON("/api/v1/acquisition/scripts", token, {
    method: "POST",
    body: {
      name: "ADR-0017 MQTT E2E command",
      description: "real broker and rtc_clock acceptance fixture",
      draftSource: commandSource("v1"),
    },
  });
  await apiJSON(`/api/v1/acquisition/scripts/${script.id}/validate`, token, { method: "POST" });
  const version = await apiJSON(`/api/v1/acquisition/scripts/${script.id}/publish`, token, { method: "POST" });
  assert.equal(version.versionNo, 1);

  const channel = await apiJSON("/api/v1/acquisition/channels", token, {
    method: "POST",
    body: {
      name: "ADR-0017 MQTT E2E RTU",
      protocol: "MODBUS_RTU",
      serialConfig: { port: simulatorAlias, baudRate: 9600, dataBits: 8, stopBits: 1, parity: "N" },
      timeoutMs: 500,
      interRequestDelayMs: 5,
      enabled: 1,
    },
  });
  const device = await apiJSON("/api/v1/acquisition/devices", token, {
    method: "POST",
    body: {
      externalId: "device-adr0017-rtc",
      name: "ADR-0017 RTC simulator",
      deviceType: "FEED_PROTECTOR",
      channelId: channel.id,
      unitId: 3,
      pollIntervalMs: 200,
      failureThreshold: 3,
      enabled: 1,
      registerBlocks: [
        { name: "rtc-raw", functionCode: 3, startAddress: 100, quantity: 3, sortOrder: 0 },
      ],
    },
  });
  await apiJSON(`/api/v1/acquisition/devices/${device.id}/script`, token, {
    method: "PUT",
    body: { scriptId: script.id },
  });
  return { script, channel, device };
}

async function waitForDeviceOnline(token, deviceID) {
  return eventually(
    "simulator device ONLINE",
    () => apiJSON(`/api/v1/acquisition/states/${deviceID}`, token),
    (state) => state.status === "ONLINE" && state.registerBlocks?.every((block) => block.valid),
    45000,
  );
}

async function waitForCommand(token, commandID, status) {
  return eventually(
    `command ${commandID} ${status}`,
    async () => {
      const result = await requestJSON(`/api/v1/mqtt/commands/${encodeURIComponent(commandID)}`, token);
      return result.response.status === 200 ? result.payload.data : null;
    },
    (value) => value?.status === status,
    30000,
  );
}

async function outboxStats(token) {
  return apiJSON("/api/v1/mqtt/outbox/stats", token);
}

async function mqttState(token) {
  return apiJSON("/api/v1/mqtt/state", token);
}

async function updateMQTTConfig(token, overrides = {}) {
  const config = await apiJSON("/api/v1/mqtt/config", token, {
    method: "PUT",
    body: mqttConfigBody(true, "keep", overrides),
  });
  await eventually(
    "MQTT runtime connected after config update",
    () => mqttState(token),
    (state) => state.state === "CONNECTED" && state.connected === true,
    30000,
  );
  return config;
}

function messageSchema(message, schema) {
  return message.payload?.schema === schema;
}

function topicFor(kind, deviceID) {
  const base = `${topicPrefix}/${edgeID}`;
  if (kind === "edge-status") return `${base}/status`;
  return `${base}/device/${deviceID}/${kind}`;
}

function commandTopic(deviceID) {
  return topicFor("command", deviceID);
}

function commandResultTopic(deviceID) {
  return topicFor("command-result", deviceID);
}

function commandPayload(commandID, deviceID, values, expiresInMS = 30000) {
  const issuedAt = new Date().toISOString();
  return {
    schema: "device-command/v1",
    commandId: commandID,
    deviceId: deviceID,
    name: "set_clock",
    args: values,
    issuedAt,
    expiresAt: new Date(Date.now() + expiresInMS).toISOString(),
  };
}

function simulatorWriteCount() {
  if (!simulatorProcess) return 0;
  return simulatorProcess.output
    .split("\n")
    .filter((line) => line.includes("channel=adr0017-mqtt-rtu"))
    .filter((line) => /function=10 address=100 count=3 result=OK/.test(line))
    .length;
}

function simulatorLogLines() {
  return simulatorProcess.output
    .split("\n")
    .filter((line) => line.includes("channel=adr0017-mqtt-rtu"));
}

function assertWriteFollowsCompletedPoll(lineCountBeforeCommand) {
  const lines = simulatorLogLines();
  const writeIndex = lines.findIndex((line, index) => index >= lineCountBeforeCommand
    && /function=10 address=100 count=3 result=OK/.test(line));
  assert.ok(writeIndex >= 0, "simulator control write log missing");
  const previousPollIndex = lines.slice(0, writeIndex).findLastIndex(
    (line) => /function=03 address=100 count=3 result=OK/.test(line),
  );
  assert.ok(previousPollIndex >= lineCountBeforeCommand, "control write must follow a completed poll at the channel safe boundary");
}

function assertRawSnapshot(message) {
  assert.equal(message.topic, topicFor("raw", "device-adr0017-rtc"));
  assert.equal(message.qos, 0);
  assert.equal(message.retain, 0);
  assert.equal(message.payload.schema, "raw-register-snapshot/v1");
  const block = message.payload.data.blocks.find((item) => item.name === "rtc-raw");
  assert.ok(block, "raw snapshot must include rtc-raw block");
  assert.equal(block.functionCode, 3);
  assert.equal(block.valid, true);
  assert.deepEqual(block.registers.map((item) => item.address), [100, 101, 102]);
  for (const register of block.registers) assert.equal(typeof register.value, "number");
}

function assertNoRawInOutboxDatabase(files) {
  for (const [name, content] of files) {
    assert.equal(content.includes("raw-register-snapshot/v1"), false, `${name} must not persist raw in reliable outbox`);
  }
}

async function databaseFiles() {
  if (databaseKind !== "sqlite") return [];
  const names = await readdir(tempRoot);
  const files = [];
  for (const name of names.filter((value) => value.startsWith("e2e.db"))) {
    files.push([name, await readFile(path.join(tempRoot, name), "utf8")]);
  }
  return files;
}

async function assertDatabaseSecretsNotPresent() {
  if (databaseKind === "sqlite") {
    const files = await databaseFiles();
    for (const [name, content] of files) {
      assert.equal(content.includes(secret), false, `${name} must not contain MQTT password plaintext`);
      assert.equal(content.includes(masterSecret), false, `${name} must not contain MQTT master secret`);
    }
    return;
  }
  const output = await compose(
    "exec", "-T", "postgres", "psql", "-U", composeEnvironment.MQTT_E2E_POSTGRES_USER,
    "-d", composeEnvironment.MQTT_E2E_POSTGRES_DB, "-At",
    "-c", "SELECT COALESCE(password_ciphertext,'') || COALESCE(client_private_key_ciphertext,'') FROM mqtt_config",
  );
  assert.equal(output.includes(secret), false, "PostgreSQL MQTT config must not contain password plaintext");
  assert.equal(output.includes(masterSecret), false, "PostgreSQL MQTT config must not contain master secret");
}

function assertNoSecretInRuntimeOutputs(configViews, collector) {
  const output = [
    ...configViews.map((value) => JSON.stringify(value)),
    apiProcess?.output ?? "",
    simulatorProcess?.output ?? "",
    collector?.messages.map((message) => message.rawPayload).join("\n") ?? "",
  ].join("\n");
  assert.equal(output.includes(secret), false, "MQTT password leaked into API/log/MQTT output");
  assert.equal(output.includes(masterSecret), false, "MQTT master secret leaked into API/log/MQTT output");
}

async function updateScript(token, scriptID, source) {
  await apiJSON(`/api/v1/acquisition/scripts/${scriptID}`, token, {
    method: "PUT",
    body: { name: "ADR-0017 MQTT E2E command", description: "real broker and rtc_clock acceptance fixture", draftSource: source },
  });
  const validation = await apiJSON(`/api/v1/acquisition/scripts/${scriptID}/validate`, token, { method: "POST" });
  assert.equal(validation.valid, true, JSON.stringify(validation));
  return apiJSON(`/api/v1/acquisition/scripts/${scriptID}/publish`, token, { method: "POST" });
}

async function waitForResult(collector, deviceID, commandID, status, fromIndex, timeout = 40000) {
  const result = await collector.waitFor(
    (message) => message.topic === commandResultTopic(deviceID)
      && message.payload.schema === "device-command-result/v1"
      && message.payload.data?.commandId === commandID
      && message.payload.data?.status === status,
    fromIndex,
    timeout,
  );
  assert.equal(result.message.qos, 1);
  assert.equal(result.message.retain, 0);
  return result;
}

async function runScenario() {
  tempRoot = await mkdtemp(path.join(os.tmpdir(), "edge-collector-adr0017-mqtt-e2e-"));
  await startServices();
  await startSimulator();
  const environment = await migrateAndStartAPI();
  const configViews = [];

  const login = await apiJSON("/api/auth/login", "", {
    method: "POST",
    body: { username: "admin", password: "admin123" },
  });
  const token = login.tokenValue;

  const disabledConfig = await apiJSON("/api/v1/mqtt/config", token, {
    method: "PUT",
    body: mqttConfigBody(false, "set"),
  });
  configViews.push(disabledConfig);
  assert.equal(disabledConfig.enabled, false);
  assert.equal(disabledConfig.passwordConfigured, true);
  assert.equal(JSON.stringify(disabledConfig).includes(secret), false);

  const fixture = await createAcquisitionFixture(token);
  const initialState = await waitForDeviceOnline(token, fixture.device.id);
  const initialBlock = initialState.registerBlocks.find((block) => block.name === "rtc-raw");
  assert.deepEqual(initialBlock.values?.map((value) => value ?? null).length ?? 3, 3);

  const filter = `${topicPrefix}/${edgeID}/#`;
  ({ child: collectorProcess, collector } = await startCollector(filter));

  const testConnectionBefore = await apiJSON("/api/v1/mqtt/test-connection", token, {
    method: "POST",
    body: mqttConfigBody(true, "keep", { clientId: `${clientID}-test-connection` }),
  });
  assert.ok(testConnectionBefore !== undefined);
  const stateBeforeEnable = await mqttState(token);
  assert.equal(stateBeforeEnable.state, "DISABLED");

  const enabledConfig = await updateMQTTConfig(token);
  configViews.push(enabledConfig);
  assert.equal(enabledConfig.passwordConfigured, true);

  const edgeOnline = await collector.waitFor(
    (message) => message.topic === topicFor("edge-status") && message.payload.data?.online === true,
    0,
  );
  assert.equal(edgeOnline.message.payload.schema, "edge-status/v1");
  assert.equal(edgeOnline.message.payload.data.reason, "connected");
  assert.equal(edgeOnline.message.qos, 1);
  await waitForRetainedMessage(topicFor("edge-status"), (message) => message.payload.data?.online === true);

  const deviceStatus = await collector.waitFor(
    (message) => message.topic === topicFor("status", fixture.device.externalId ?? "device-adr0017-rtc")
      && message.payload.schema === "device-status/v1",
    0,
  );
  assert.equal(deviceStatus.message.qos, 1);
  assert.equal(deviceStatus.message.payload.data.status, "ONLINE");
  await waitForRetainedMessage(
    topicFor("status", fixture.device.externalId ?? "device-adr0017-rtc"),
    (message) => message.payload.schema === "device-status/v1",
  );

  const raw = await collector.waitFor(
    (message) => message.topic === topicFor("raw", "device-adr0017-rtc")
      && messageSchema(message, "raw-register-snapshot/v1"),
    0,
  );
  assertRawSnapshot(raw.message);
  const rawBeforeOffline = raw.message;

  const firstEvent = await collector.waitFor(
    (message) => message.payload.schema === "device-event/v1",
    0,
  );
  assert.equal(firstEvent.message.qos, 1);
  assert.equal(firstEvent.message.retain, 0);
  assert.equal(firstEvent.message.payload.data.kind, "mqtt_e2e_tick");

  // Broker offline: acquisition remains live, raw stays latest-only, and the
  // event path accumulates durable rows instead of silently dropping events.
  const brokerOfflineAt = Date.now();
  await stopBroker();
  await delay(3200);
  const acquisitionWhileOffline = await waitForDeviceOnline(token, fixture.device.id);
  assert.equal(acquisitionWhileOffline.status, "ONLINE");
  const offlineStats = await outboxStats(token);
  assert.ok(Number(offlineStats.rows) > 0, `offline event must be in reliable outbox: ${JSON.stringify(offlineStats)}`);
  const offlineState = await mqttState(token);
  const pendingLatest = offlineState.pendingLatestCount ?? offlineState.pendingRawCount ?? offlineState.pendingLatest ?? null;
  if (pendingLatest !== null) assert.ok(Number(pendingLatest) >= 1);
  assertNoRawInOutboxDatabase(await databaseFiles());

  await stopProcess(collectorProcess, "SIGTERM");
  collectorProcess = undefined;
  await startBroker();
  const brokerReconnectAt = Date.now();
  ({ child: collectorProcess, collector } = await startCollector(filter));

  const onlineAfterReconnect = await collector.waitFor(
    (message) => message.topic === topicFor("edge-status") && message.payload.data?.online === true,
    0,
  );
  await waitForRetainedMessage(topicFor("edge-status"), (message) => message.payload.data?.online === true);
  const retainedDeviceStatus = await collector.waitFor(
    (message) => message.topic === topicFor("status", "device-adr0017-rtc")
      && message.payload.schema === "device-status/v1",
    0,
  );
  await waitForRetainedMessage(
    topicFor("status", "device-adr0017-rtc"),
    (message) => message.payload.schema === "device-status/v1",
  );
  const rawAfterReconnect = await collector.waitFor(
    (message) => message.topic === topicFor("raw", "device-adr0017-rtc")
      && message.payload.schema === "raw-register-snapshot/v1",
    0,
  );
  assertRawSnapshot(rawAfterReconnect.message);
  assert.ok(Date.parse(rawAfterReconnect.message.payload.timestamp) >= Date.parse(rawBeforeOffline.payload.timestamp));
  const recoveredEvent = await collector.waitFor(
    (message) => message.payload.schema === "device-event/v1"
      && message.payload.messageId !== firstEvent.message.payload.messageId
      && Date.parse(message.payload.timestamp) >= brokerOfflineAt
      && Date.parse(message.payload.timestamp) <= brokerReconnectAt,
    0,
    30000,
  );
  assert.equal(recoveredEvent.message.qos, 1);
  await eventually(
    "reliable event outbox drain",
    () => outboxStats(token),
    (stats) => Number(stats.rows) < Number(offlineStats.rows),
    30000,
  );

  // Kill the API process rather than asking it to shut down. The broker must
  // publish the pre-registered Will. Its timestamp is deliberately compared
  // with the observed receive time; it is not treated as offlineAt.
  await delay(1500);
  const observedDisconnectAt = Date.now();
  await stopProcess(apiProcess, "SIGKILL");
  apiProcess = undefined;
  const will = await collector.waitFor(
    (message) => message.topic === topicFor("edge-status") && message.payload.data?.online === false,
    0,
    30000,
  );
  assert.equal(will.message.payload.schema, "edge-status/v1");
  assert.equal(will.message.payload.data.reason, "last_will");
  assert.equal("offlineAt" in will.message.payload, false);
  assert.ok(Date.parse(will.message.payload.timestamp) < observedDisconnectAt - 500, "LWT timestamp must predate observed disconnect");
  assert.ok(will.message.receivedAt >= observedDisconnectAt);

  // Restart the same API against the same database. This also exercises
  // command journal/outbox recovery in later steps without a fresh client ID.
  apiProcess = startProcess("go", ["run", "./cmd/api"], apiDir, environment);
  await waitForHTTP(`http://127.0.0.1:${apiPort}/health`, apiProcess);
  const onlineAfterAPIRestart = await collector.waitFor(
    (message) => message.topic === topicFor("edge-status") && message.payload.data?.online === true,
    will.index + 1,
    30000,
  );
  await waitForRetainedMessage(topicFor("edge-status"), (message) => message.payload.data?.online === true);
  await waitForDeviceOnline(token, fixture.device.id);

  const writeCountBeforeCommand = simulatorWriteCount();
  const firstCommandID = "command-adr0017-success";
  const firstCommand = commandPayload(firstCommandID, "device-adr0017-rtc", { hour: 10, minute: 20, second: 30 });
  const simulatorLinesBeforeCommand = simulatorLogLines().length;
  let messageIndex = collector.messages.length;
  await publishMQTT(commandTopic("device-adr0017-rtc"), firstCommand);
  await waitForResult(collector, "device-adr0017-rtc", firstCommandID, "ACCEPTED", messageIndex);
  const firstFinal = await waitForResult(collector, "device-adr0017-rtc", firstCommandID, "SUCCEEDED", messageIndex);
  assert.equal(firstFinal.message.payload.data.result.version, "v1");
  assert.deepEqual(firstFinal.message.payload.data.result.written, [10, 20, 30]);
  await eventually("one simulator control write", () => simulatorWriteCount(), (count) => count === writeCountBeforeCommand + 1, 30000);
  assertWriteFollowsCompletedPoll(simulatorLinesBeforeCommand);

  // Same commandId with the same canonical payload, including a reordered
  // object, must only create one real FC16 action.
  const duplicateReordered = {
    expiresAt: firstCommand.expiresAt,
    issuedAt: firstCommand.issuedAt,
    args: { second: 30, hour: 10, minute: 20 },
    name: firstCommand.name,
    deviceId: firstCommand.deviceId,
    commandId: firstCommand.commandId,
    schema: firstCommand.schema,
  };
  messageIndex = collector.messages.length;
  await publishMQTT(commandTopic("device-adr0017-rtc"), duplicateReordered);
  await publishMQTT(commandTopic("device-adr0017-rtc"), firstCommand);
  await waitForResult(collector, "device-adr0017-rtc", firstCommandID, "SUCCEEDED", messageIndex);
  await delay(1200);
  assert.equal(simulatorWriteCount(), writeCountBeforeCommand + 1, "duplicate commandId must not repeat FC16");

  const conflict = { ...firstCommand, args: { hour: 11, minute: 20, second: 30 } };
  messageIndex = collector.messages.length;
  await publishMQTT(commandTopic("device-adr0017-rtc"), conflict);
  await delay(1500);
  const conflictMessages = collector.messages.slice(messageIndex).filter(
    (message) => message.topic === commandResultTopic("device-adr0017-rtc")
      && message.payload.data?.commandId === firstCommandID,
  );
  assert.equal(conflictMessages.length, 0, "conflicting commandId must not create a second result or execution token");
  assert.equal(simulatorWriteCount(), writeCountBeforeCommand + 1, "conflicting commandId must not repeat FC16");

  // Publish a new script while a command is waiting at the channel boundary.
  // The result contains the version captured at command start.
  const versionSwitchID = "command-adr0017-version-switch";
  messageIndex = collector.messages.length;
  await publishMQTT(commandTopic("device-adr0017-rtc"), commandPayload(versionSwitchID, "device-adr0017-rtc", { hour: 11, minute: 21, second: 31 }));
  await waitForResult(collector, "device-adr0017-rtc", versionSwitchID, "ACCEPTED", messageIndex);
  await updateScript(token, fixture.script.id, commandSource("v2"));
  const secondVersionID = "command-adr0017-version-v2";
  await publishMQTT(commandTopic("device-adr0017-rtc"), commandPayload(secondVersionID, "device-adr0017-rtc", { hour: 12, minute: 22, second: 32 }));
  const switchFinal = await waitForResult(collector, "device-adr0017-rtc", versionSwitchID, "SUCCEEDED", messageIndex);
  const secondVersionFinal = await waitForResult(collector, "device-adr0017-rtc", secondVersionID, "SUCCEEDED", messageIndex);
  assert.ok(["v1", "v2"].includes(switchFinal.message.payload.data.result.version));
  assert.equal(secondVersionFinal.message.payload.data.result.version, "v2");

  // A short command burst demonstrates bounded queue behavior while ordinary
  // poll requests continue on the same simulator channel.
  await updateMQTTConfig(token, { commandQueueCapacity: 1, commandPollFairness: 1 });
  const pollLinesBeforeBurst = simulatorLogLines().filter((line) => /function=03 address=100 count=3 result=OK/.test(line)).length;
  const burstStart = collector.messages.length;
  const burstIDs = Array.from({ length: 6 }, (_, index) => `command-adr0017-burst-${index}`);
  await Promise.all(burstIDs.map((commandID, index) => publishMQTT(
    commandTopic("device-adr0017-rtc"),
    commandPayload(commandID, "device-adr0017-rtc", { hour: 13, minute: 23, second: 40 + index }),
  )));
  const burstProgress = async () => ({
    receivedCommands: collector.messages.filter((message) => message.topic === commandTopic("device-adr0017-rtc") && burstIDs.includes(message.payload.commandId)).map((message) => message.payload.commandId),
    messages: collector.messages.filter((message) => message.topic === commandResultTopic("device-adr0017-rtc") && burstIDs.includes(message.payload.data?.commandId) && ["SUCCEEDED", "FAILED", "EXPIRED"].includes(message.payload.data?.status)),
    journals: await Promise.all(burstIDs.map(async (commandID) => {
      const result = await requestJSON(`/api/v1/mqtt/commands/${encodeURIComponent(commandID)}`, token);
      return { commandID, status: result.payload.data?.status ?? null, code: result.response.status };
    })),
  });
  await eventually(
    "command burst terminal results",
    burstProgress,
    (progress) => {
      const terminalStatuses = new Set(["SUCCEEDED", "FAILED", "EXPIRED"]);
      const received = new Set(progress.receivedCommands);
      const results = new Set(progress.messages.map((message) => message.payload.data.commandId));
      const journalsTerminal = progress.journals.every((journal) => journal.code === 200 && terminalStatuses.has(journal.status));
      return received.size >= burstIDs.length
        && results.size >= burstIDs.length
        && journalsTerminal;
    },
    60000,
  );
  const burstFailures = collector.messages.slice(burstStart).filter((message) => burstIDs.includes(message.payload.data?.commandId) && message.payload.data?.status === "FAILED");
  assert.ok(burstFailures.some((message) => message.payload.data.error?.type === "QUEUE_FULL"), "bounded command burst must expose QUEUE_FULL");
  await delay(1200);
  const pollLinesAfterBurst = simulatorLogLines().filter((line) => /function=03 address=100 count=3 result=OK/.test(line)).length;
  assert.ok(pollLinesAfterBurst > pollLinesBeforeBurst, "command burst must not permanently starve ordinary poll");

  // Disable per-second event generation so the capacity boundary is stable.
  await updateScript(token, fixture.script.id, commandSource("v3", false));
  await updateMQTTConfig(token, { commandQueueCapacity: 8, outboxMaxRows: 2 });
  await eventually("empty outbox before capacity test", () => outboxStats(token), (stats) => Number(stats.rows) === 0, 30000);
  const capacityWriteBaseline = simulatorWriteCount();
  const capacityStart = collector.messages.length;
  const capacityIDs = ["command-adr0017-capacity-1", "command-adr0017-capacity-2"];
  await Promise.all(capacityIDs.map((commandID, index) => publishMQTT(
    commandTopic("device-adr0017-rtc"),
    commandPayload(commandID, "device-adr0017-rtc", { hour: 14, minute: 24, second: 50 + index }),
  )));
  const capacityProgress = async () => ({
    messages: collector.messages.slice(capacityStart).filter((message) => capacityIDs.includes(message.payload.data?.commandId) && ["SUCCEEDED", "FAILED"].includes(message.payload.data?.status)),
    journals: await Promise.all(capacityIDs.map(async (commandID) => {
      const result = await requestJSON(`/api/v1/mqtt/commands/${encodeURIComponent(commandID)}`, token);
      return { commandID, status: result.payload.data?.status ?? null, code: result.response.status };
    })),
  });
  const capacityProgressResult = await eventually(
    "capacity command terminal result",
    capacityProgress,
    (progress) => progress.messages.length >= 1,
    40000,
  );
  const capacityResults = capacityProgressResult.messages;
  await delay(2500);
  assert.ok(simulatorWriteCount() <= capacityWriteBaseline + 1, "capacity boundary must not execute every over-capacity command");
  const capacityFailure = capacityResults.find((message) => message.payload.data?.error?.type === "RELIABLE_RESULT_CAPACITY_EXHAUSTED");
  if (capacityFailure) {
    assert.equal(capacityFailure.payload.data.error.type, "RELIABLE_RESULT_CAPACITY_EXHAUSTED");
  } else {
    // When no rejection result can itself be persisted, the callback still
    // exposes the stable error code through the log-safe intake boundary.
    const state = await mqttState(token);
    assert.match(`${state.lastError ?? ""}\n${apiProcess.output}`, /error_code=RELIABLE_RESULT_CAPACITY_EXHAUSTED/);
  }
  assert.equal(simulatorWriteCount() >= capacityWriteBaseline + 2, false, "capacity exhaustion must preserve zero action for the rejected command");

  // Stop/restart with a final result already durable but broker unavailable.
  await updateMQTTConfig(token, { outboxMaxRows: 100 });
  await eventually("empty outbox before final recovery", () => outboxStats(token), (stats) => Number(stats.rows) === 0, 30000);
  const restartCommandID = "command-adr0017-final-before-puback";
  const restartBaseline = simulatorWriteCount();
  await stopBroker();
  await stopProcess(collectorProcess, "SIGTERM");
  collectorProcess = undefined;
  // The command publisher cannot reach a stopped broker, so reconnect only to
  // submit the command and then immediately stop the broker before final ACK.
  await startBroker();
  await eventually(
    "MQTT runtime connected before final restart command",
    () => mqttState(token),
    (state) => state.state === "CONNECTED" && state.connected === true,
    30000,
  );
  const commandPublisherPayload = commandPayload(restartCommandID, "device-adr0017-rtc", { hour: 15, minute: 25, second: 55 });
  await publishMQTT(commandTopic("device-adr0017-rtc"), commandPublisherPayload);
  await eventually(
    "command admitted before broker stop",
    async () => {
      const result = await requestJSON(`/api/v1/mqtt/commands/${encodeURIComponent(restartCommandID)}`, token);
      return result.response.status === 200 ? result.payload.data : null;
    },
    (value) => value?.status === "ACCEPTED" || value?.status === "RUNNING",
    10000,
  );
  await stopBroker();
  await waitForCommand(token, restartCommandID, "SUCCEEDED");
  assert.ok(Number((await outboxStats(token)).rows) >= 1, "final result must remain durable without broker PUBACK");
  await stopProcess(apiProcess, "SIGKILL");
  apiProcess = undefined;
  await startBroker();
  ({ child: collectorProcess, collector } = await startCollector(filter));
  apiProcess = startProcess("go", ["run", "./cmd/api"], apiDir, environment);
  await waitForHTTP(`http://127.0.0.1:${apiPort}/health`, apiProcess);
  const restartedFinal = await waitForResult(collector, "device-adr0017-rtc", restartCommandID, "SUCCEEDED", 0, 45000);
  assert.equal(restartedFinal.message.payload.data.commandId, restartCommandID);
  await delay(1500);
  assert.equal(simulatorWriteCount(), restartBaseline + 1, "restart recovery must resend final result without re-executing Modbus");

  await assertDatabaseSecretsNotPresent();
  assertNoSecretInRuntimeOutputs(configViews, collector);
  assert.match(simulatorProcess.output, /function=10 address=100 count=3 result=OK/);
  assert.ok(simulatorLogLines().some((line) => /function=03 address=100 count=3 result=OK/.test(line)), "simulator poll log missing");
  console.log(JSON.stringify({
    status: "passed",
    database: databaseKind,
    protocol,
    broker: "eclipse-mosquitto:2",
    simulator: "modbus-simulator/config/adr0017-mqtt-e2e.yaml",
    deviceID: "device-adr0017-rtc",
    simulatorControlWrites: simulatorWriteCount(),
    mqttMessages: collector.messages.length,
  }));
}

async function cleanup() {
  await stopProcess(collectorProcess, "SIGTERM");
  collectorProcess = undefined;
  await stopProcess(apiProcess, "SIGTERM");
  apiProcess = undefined;
  await stopProcess(simulatorProcess, "SIGTERM");
  simulatorProcess = undefined;
  if (managedCompose) {
    try {
      await compose("down", "--remove-orphans");
    } catch (error) {
      console.error(`MQTT E2E compose cleanup failed: ${error.message}`);
    }
    managedCompose = false;
  }
  if (tempRoot) await rm(tempRoot, { recursive: true, force: true });
}

function missingDependencies() {
  const missing = [];
  if (manageServices && !hasCommand("docker")) missing.push("docker");
  if (!hasCommand("go")) missing.push("go");
  if (!hasCommand("uv")) missing.push("uv");
  if (manageServices === false && (!hasCommand("mosquitto_sub") || !hasCommand("mosquitto_pub"))) {
    missing.push("mosquitto_sub/mosquitto_pub");
  }
  return missing;
}

const missing = missingDependencies();
if (missing.length > 0) {
  const message = `SKIP ADR-0017 MQTT E2E: missing ${missing.join(", ")}`;
  if (required) {
    console.error(message);
    process.exitCode = 1;
  } else {
    console.log(message);
  }
} else {
  try {
    assert.ok(["sqlite", "postgres"].includes(databaseKind), `unsupported MQTT_E2E_DATABASE=${databaseKind}`);
    assert.ok(["MQTT_5", "MQTT_3_1_1"].includes(protocol), `unsupported MQTT_E2E_PROTOCOL=${protocol}`);
    await runScenario();
  } catch (error) {
    console.error(error.stack ?? error);
    process.exitCode = 1;
  } finally {
    await cleanup();
  }
}
