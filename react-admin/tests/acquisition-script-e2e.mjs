import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const repoRoot = fileURLToPath(new URL("../../", import.meta.url));
const apiDir = path.join(repoRoot, "edge-collector-api");
const simulatorDir = path.join(repoRoot, "modbus-simulator");
const apiPort = Number(process.env.ACQUISITION_SCRIPT_E2E_API_PORT ?? 8110);
const webPort = Number(process.env.ACQUISITION_SCRIPT_E2E_WEB_PORT ?? 4182);
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
  if (code !== 0) {
    throw new Error(`${command} ${args.join(" ")} failed (${code}):\n${child.output}`);
  }
}

async function requestJSON(pathname, token, options = {}) {
  const headers = {
    ...(options.body !== undefined ? { "Content-Type": "application/json" } : {}),
    ...(token ? { Authorization: token } : {}),
    ...(options.headers ?? {}),
  };
  const response = await fetch(`${apiBase}${pathname}`, {
    ...options,
    headers,
    body: options.body !== undefined && typeof options.body !== "string"
      ? JSON.stringify(options.body)
      : options.body,
  });
  const payload = await response.json();
  return { response, payload };
}

async function apiJSON(pathname, token, options = {}) {
  const { response, payload } = await requestJSON(pathname, token, options);
  assert.equal(response.status, 200, `${options.method ?? "GET"} ${pathname}: ${JSON.stringify(payload)}`);
  assert.equal(payload.code, 200, `${options.method ?? "GET"} ${pathname}: ${JSON.stringify(payload)}`);
  return payload.data;
}

function isBrowserResponse(response, method, pathname) {
  const url = new URL(response.url());
  return response.request().method() === method && url.pathname === pathname;
}

async function waitForDeviceOnline(token, deviceID, timeout = 30000) {
  const deadline = Date.now() + timeout;
  let lastState;
  while (Date.now() < deadline) {
    lastState = await apiJSON(`/api/v1/acquisition/states/${deviceID}`, token);
    if (lastState.status === "ONLINE" && lastState.registerBlocks.every((block) => block.valid)) {
      return lastState;
    }
    await delay(200);
  }
  throw new Error(`device ${deviceID} did not become ONLINE: ${JSON.stringify(lastState)}`);
}

async function waitForScriptState(token, deviceID, versionNo, eventKey, timeout = 30000) {
  const deadline = Date.now() + timeout;
  let lastState;
  while (Date.now() < deadline) {
    const states = await apiJSON("/api/v1/acquisition/script-states", token);
    lastState = states.find((state) => state.deviceId === deviceID);
    if (
      lastState
      && lastState.versionNo === versionNo
      && lastState.lastSuccessAt
      && lastState.state?.marker === `v${versionNo}`
      && lastState.events.some((event) => event.kind === "script_e2e" && event.key === eventKey)
    ) {
      return lastState;
    }
    await delay(200);
  }
  throw new Error(`script state did not reach v${versionNo}/${eventKey}: ${JSON.stringify(lastState)}`);
}

async function waitForScriptError(token, deviceID, versionNo, expectedType = "SCRIPT_RUNTIME", timeout = 30000) {
  const deadline = Date.now() + timeout;
  let lastState;
  while (Date.now() < deadline) {
    lastState = await apiJSON(`/api/v1/acquisition/script-states/${deviceID}`, token);
    if (
      lastState.versionNo === versionNo
      && lastState.lastError
      && lastState.lastErrorType === expectedType
      && !lastState.lastSuccessAt
    ) {
      return lastState;
    }
    await delay(200);
  }
  throw new Error(`script state did not reach v${versionNo} ${expectedType}: ${JSON.stringify(lastState)}`);
}

async function openScriptEditor(page, scriptName) {
  const row = page.locator("tbody tr").filter({ hasText: scriptName }).first();
  await row.waitFor();
  await row.getByRole("button", { name: "编辑", exact: true }).click();
  const dialog = page.getByRole("dialog").filter({ hasText: `编辑协议脚本 · ${scriptName}` }).last();
  await dialog.getByRole("heading", { name: `编辑协议脚本 · ${scriptName}`, exact: true }).waitFor();
  await dialog.locator("#script-source").waitFor();
  return dialog;
}

async function saveScript(page, dialog, scriptID, source) {
  await dialog.locator("#script-source").fill(source);
  const responsePromise = page.waitForResponse((response) => isBrowserResponse(
    response,
    "PUT",
    `/api/v1/acquisition/scripts/${scriptID}`,
  ));
  await dialog.getByRole("button", { name: "保存草稿", exact: true }).click();
  const response = await responsePromise;
  assert.equal(response.status(), 200, `save script ${scriptID} failed`);
}

async function validateScript(page, dialog, scriptID) {
  const responsePromise = page.waitForResponse((response) => isBrowserResponse(
    response,
    "POST",
    `/api/v1/acquisition/scripts/${scriptID}/validate`,
  ));
  await dialog.getByRole("button", { name: "Validate Draft", exact: true }).click();
  return responsePromise;
}

async function publishScript(page, dialog, scriptID, versionNo) {
  const responsePromise = page.waitForResponse((response) => isBrowserResponse(
    response,
    "POST",
    `/api/v1/acquisition/scripts/${scriptID}/publish`,
  ));
  await dialog.getByRole("button", { name: "Publish", exact: true }).click();
  const response = await responsePromise;
  assert.equal(response.status(), 200, `publish script ${scriptID} failed`);
  await page.getByText(`脚本已发布 v${versionNo}`, { exact: true }).waitFor();
}

async function closeDialog(dialog) {
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
}

async function updateDeviceScript(page, deviceID, deviceName, scriptID) {
  const row = page.locator("tbody tr").filter({ hasText: deviceName }).first();
  await row.waitFor();
  await row.getByRole("button", { name: "编辑", exact: true }).click();
  const dialog = page.getByRole("dialog").filter({ hasText: "编辑采集设备" }).last();
  await dialog.getByRole("heading", { name: "编辑采集设备", exact: true }).waitFor();
  const scriptSelect = dialog.locator('select[name="scriptId"]');
  await scriptSelect.waitFor();
  if (scriptID === null) {
    await scriptSelect.selectOption("");
  } else {
    await scriptSelect.locator(`option[value="${scriptID}"]`).waitFor({ state: "attached" });
    await scriptSelect.selectOption(String(scriptID));
  }
  const responsePromise = page.waitForResponse((response) => isBrowserResponse(
    response,
    "PUT",
    `/api/v1/acquisition/devices/${deviceID}`,
  ));
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  const response = await responsePromise;
  assert.equal(response.status(), 200, `update device ${deviceID} failed`);
  await dialog.waitFor({ state: "hidden" });
}

const sourceV1 = `def after_poll(ctx):
    ctx.state_set("marker", "v1")
    ctx.emit_event("script_e2e", "v1", {"marker": "v1"})
`;
const sourceV2 = `def after_poll(ctx):
    ctx.state_set("marker", "v2")
    ctx.emit_event("script_e2e", "v2", {"marker": "v2"})
`;
const sourceV3 = `def after_poll(ctx):
    ctx.state_set("marker", "v3")
    ctx.emit_event("script_e2e", "v3", {"marker": "v3"})
`;
const sourceRuntimeError = `def after_poll(ctx):
    ctx.read_holding(65535, 2)
`;
const sourceRuntimeLimit = `def after_poll(ctx):
    for i in range(200000):
        pass
`;

const tempRoot = await mkdtemp(path.join(os.tmpdir(), "edge-collector-adr0016-script-e2e-"));
const environment = {
  APP_ENV: "dev",
  APP_CONFIG_PROFILE: "sqlite",
  APP_DATABASE__URL: path.join(tempRoot, "e2e.db"),
  APP_FILE__STORAGE_ROOT: path.join(tempRoot, "uploads"),
  APP_HTTP__ADDRESS: `127.0.0.1:${apiPort}`,
  APP_JWT__SECRET: "edge-collector-adr0016-script-e2e-secret",
  APP_LOG__LEVEL: "warn",
  GOCACHE: path.join(tempRoot, "go-build"),
};

let browser;
try {
  const simulator = startProcess(
    "uv",
    ["run", "modbus-simulator", "--config", "config/adr0015-e2e.yaml"],
    simulatorDir,
  );
  await waitForOutput(simulator, "Modbus Simulator Started");
  await waitForPort(2502);

  await runCommand("go", ["run", "./cmd/migrate", "up", "--kind", "all"], apiDir, environment);
  const api = startProcess("go", ["run", "./cmd/api"], apiDir, environment);
  await waitForHTTP(`${apiBase}/health`, api);
  const web = startProcess(
    "node",
    ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(webPort)],
    path.join(repoRoot, "react-admin"),
    { VITE_API_BASE_URL: apiBase },
  );
  await waitForHTTP(`${webBase}/`, web);

  const login = await apiJSON("/api/auth/login", "", {
    method: "POST",
    body: { username: "admin", password: "admin123" },
  });
  const token = login.tokenValue;

  const scriptName = "E2E 动态事务脚本";
  const script = await apiJSON("/api/v1/acquisition/scripts", token, {
    method: "POST",
    body: {
      name: scriptName,
      description: "验证 API 与浏览器脚本闭环",
      draftSource: "x = 1\n",
    },
  });
  const listed = await apiJSON("/api/v1/acquisition/scripts?page=1&pageSize=20", token);
  assert.ok(listed.records.some((item) => item.id === script.id && item.name === scriptName));

  browser = await chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH }
      : {}),
  });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  await page.addInitScript(
    (value) => localStorage.setItem("react-admin-auth", JSON.stringify({ token: value, user: null })),
    token,
  );

  await page.goto(`${webBase}/acquisition/script`, { waitUntil: "networkidle" });
  await page.getByRole("heading", { name: "协议脚本", exact: true }).waitFor();
  await page.getByText(scriptName, { exact: true }).waitFor();
  const scriptDialog = await openScriptEditor(page, scriptName);
  assert.equal(await scriptDialog.locator("#script-source").inputValue(), "x = 1\n");

  const invalidResponse = await validateScript(page, scriptDialog, script.id);
  assert.equal(invalidResponse.status(), 200);
  const invalidPayload = await invalidResponse.json();
  assert.equal(invalidPayload.code, 200);
  assert.equal(invalidPayload.data.valid, false);
  assert.equal(invalidPayload.data.errors[0].line, 1);
  assert.equal(invalidPayload.data.errors[0].column, 1);
  await page.getByText("Draft 校验未通过", { exact: true }).waitFor();
  await page.getByText("第 1 行，第 1 列", { exact: true }).waitFor();
  await page.getByText("Draft 校验未通过，请根据行号和列号修正源码", { exact: true }).waitFor();

  await saveScript(page, scriptDialog, script.id, sourceV1);
  const validResponse = await validateScript(page, scriptDialog, script.id);
  assert.equal(validResponse.status(), 200);
  const validPayload = await validResponse.json();
  assert.equal(validPayload.code, 200);
  assert.equal(validPayload.data.valid, true);
  await page.getByText("Draft 校验通过", { exact: true }).waitFor();
  await publishScript(page, scriptDialog, script.id, 1);
  await closeDialog(scriptDialog);

  const publishedV1 = await apiJSON(`/api/v1/acquisition/scripts/${script.id}`, token);
  assert.equal(publishedV1.publishedVersion.versionNo, 1);
  assert.equal(publishedV1.draftMatchesPublished, true);

  const channel = await apiJSON("/api/v1/acquisition/channels", token, {
    method: "POST",
    body: {
      name: "E2E Script TCP",
      protocol: "MODBUS_TCP",
      timeoutMs: 500,
      interRequestDelayMs: 5,
      enabled: 1,
    },
  });
  const deviceName = "E2E Script Device";
  const device = await apiJSON("/api/v1/acquisition/devices", token, {
    method: "POST",
    body: {
      name: deviceName,
      deviceType: "FEED_PROTECTOR",
      channelId: channel.id,
      unitId: 1,
      networkEndpoint: { host: "127.0.0.1", port: 2502 },
      pollIntervalMs: 200,
      failureThreshold: 2,
      enabled: 1,
      registerBlocks: [
        { name: "holding", functionCode: 3, startAddress: 0, quantity: 2, sortOrder: 0 },
      ],
    },
  });
  await waitForDeviceOnline(token, device.id);

  await page.goto(`${webBase}/acquisition/device`, { waitUntil: "networkidle" });
  await page.getByRole("heading", { name: "设备管理", exact: true }).waitFor();
  await page.getByText(deviceName, { exact: true }).waitFor();
  await updateDeviceScript(page, device.id, deviceName, script.id);
  const boundDevice = await apiJSON(`/api/v1/acquisition/devices/${device.id}`, token);
  assert.equal(boundDevice.scriptId, script.id);

  const stateV1 = await waitForScriptState(token, device.id, 1, "v1");
  assert.equal(stateV1.state.marker, "v1");

  await page.goto(`${webBase}/acquisition/script`, { waitUntil: "networkidle" });
  await page.getByRole("heading", { name: "协议脚本", exact: true }).waitFor();
  const boundScriptRow = page.locator("tbody tr").filter({ hasText: scriptName }).first();
  await boundScriptRow.getByText("1 台", { exact: true }).waitFor();
  await boundScriptRow.getByRole("button", { name: "运行状态", exact: true }).click();
  const runtimeDialog = page.getByRole("dialog").filter({ hasText: `${scriptName} · 运行状态` }).last();
  await runtimeDialog.getByRole("heading", { name: `${scriptName} · 运行状态`, exact: true }).waitFor();
  await runtimeDialog.getByText(deviceName, { exact: true }).waitFor();
  await runtimeDialog.getByText(`v1 · #${stateV1.scriptVersionId}`, { exact: true }).waitFor();
  await runtimeDialog.getByText("script_e2e / v1", { exact: true }).waitFor();
  await runtimeDialog.getByRole("button", { name: "关闭详情弹窗" }).click();

  const errorDialog = await openScriptEditor(page, scriptName);
  await saveScript(page, errorDialog, script.id, sourceRuntimeError);
  const draftError = await apiJSON(`/api/v1/acquisition/scripts/${script.id}`, token);
  assert.equal(draftError.draftMatchesPublished, false);
  const stateDuringDraft = await apiJSON(`/api/v1/acquisition/script-states/${device.id}`, token);
  assert.equal(stateDuringDraft.versionNo, 1);
  assert.equal(stateDuringDraft.state.marker, "v1");

  const validErrorResponse = await validateScript(page, errorDialog, script.id);
  assert.equal(validErrorResponse.status(), 200);
  const validErrorPayload = await validErrorResponse.json();
  assert.equal(validErrorPayload.code, 200);
  assert.equal(validErrorPayload.data.valid, true);
  await page.getByText("Draft 校验通过", { exact: true }).waitFor();
  await publishScript(page, errorDialog, script.id, 2);
  await closeDialog(errorDialog);

  const stateError = await waitForScriptError(token, device.id, 2);
  assert.match(stateError.lastError, /ctx\.read_holding|address range/);
  const staticAfterError = await apiJSON(`/api/v1/acquisition/states/${device.id}`, token);
  assert.equal(staticAfterError.status, "ONLINE");
  assert.equal(staticAfterError.registerBlocks[0].valid, true);

  const errorScriptRow = page.locator("tbody tr").filter({ hasText: scriptName }).first();
  await errorScriptRow.getByRole("button", { name: "运行状态", exact: true }).click();
  const errorRuntimeDialog = page.getByRole("dialog").filter({ hasText: `${scriptName} · 运行状态` }).last();
  await errorRuntimeDialog.getByText(deviceName, { exact: true }).waitFor();
  await errorRuntimeDialog.getByText("v2 · #" + stateError.scriptVersionId, { exact: true }).waitFor();
  await errorRuntimeDialog.getByText("最近失败", { exact: true }).waitFor();
  await errorRuntimeDialog.getByText("脚本运行", { exact: true }).waitFor();
  await errorRuntimeDialog.getByText(/ctx\.read_holding|address range/).waitFor();
  await errorRuntimeDialog.getByRole("button", { name: "关闭详情弹窗" }).click();

  const v3Dialog = await openScriptEditor(page, scriptName);
  await saveScript(page, v3Dialog, script.id, sourceV3);
  const draftV3 = await apiJSON(`/api/v1/acquisition/scripts/${script.id}`, token);
  assert.equal(draftV3.draftMatchesPublished, false);
  const stateDuringDraftV3 = await apiJSON(`/api/v1/acquisition/script-states/${device.id}`, token);
  assert.equal(stateDuringDraftV3.versionNo, 2);
  assert.match(stateDuringDraftV3.lastError, /ctx\.read_holding|address range/);

  const validV3Response = await validateScript(page, v3Dialog, script.id);
  assert.equal(validV3Response.status(), 200);
  const validV3Payload = await validV3Response.json();
  assert.equal(validV3Payload.code, 200);
  assert.equal(validV3Payload.data.valid, true);
  await page.getByText("Draft 校验通过", { exact: true }).waitFor();
  await publishScript(page, v3Dialog, script.id, 3);
  await closeDialog(v3Dialog);

  const publishedV3 = await apiJSON(`/api/v1/acquisition/scripts/${script.id}`, token);
  assert.equal(publishedV3.publishedVersion.versionNo, 3);
  const stateV3 = await waitForScriptState(token, device.id, 3, "v3");
  assert.equal(stateV3.state.marker, "v3");

  const limitDialog = await openScriptEditor(page, scriptName);
  await saveScript(page, limitDialog, script.id, sourceRuntimeLimit);
  const stateDuringLimitDraft = await apiJSON(`/api/v1/acquisition/script-states/${device.id}`, token);
  assert.equal(stateDuringLimitDraft.versionNo, 3);
  assert.equal(stateDuringLimitDraft.state.marker, "v3");
  const validLimitResponse = await validateScript(page, limitDialog, script.id);
  assert.equal(validLimitResponse.status(), 200);
  const validLimitPayload = await validLimitResponse.json();
  assert.equal(validLimitPayload.code, 200);
  assert.equal(validLimitPayload.data.valid, true);
  await page.getByText("Draft 校验通过", { exact: true }).waitFor();
  await publishScript(page, limitDialog, script.id, 4);
  await closeDialog(limitDialog);

  const stateLimit = await waitForScriptError(token, device.id, 4, "SCRIPT_LIMIT");
  assert.match(stateLimit.lastError, /step limit/);
  const staticAfterLimit = await apiJSON(`/api/v1/acquisition/states/${device.id}`, token);
  assert.equal(staticAfterLimit.status, "ONLINE");
  assert.equal(staticAfterLimit.registerBlocks[0].valid, true);

  const limitScriptRow = page.locator("tbody tr").filter({ hasText: scriptName }).first();
  await limitScriptRow.getByRole("button", { name: "运行状态", exact: true }).click();
  const limitRuntimeDialog = page.getByRole("dialog").filter({ hasText: `${scriptName} · 运行状态` }).last();
  await limitRuntimeDialog.getByText(deviceName, { exact: true }).waitFor();
  await limitRuntimeDialog.getByText("v4 · #" + stateLimit.scriptVersionId, { exact: true }).waitFor();
  await limitRuntimeDialog.getByText("最近失败", { exact: true }).waitFor();
  await limitRuntimeDialog.getByText("资源限制", { exact: true }).waitFor();
  await limitRuntimeDialog.getByText(/step limit/).waitFor();
  await limitRuntimeDialog.getByRole("button", { name: "关闭详情弹窗" }).click();

  const versionsFromAPI = await apiJSON(`/api/v1/acquisition/scripts/${script.id}/versions`, token);
  assert.deepEqual(
    versionsFromAPI.map((version) => version.versionNo).sort((left, right) => left - right),
    [1, 2, 3, 4],
  );

  const versionRowScript = page.locator("tbody tr").filter({ hasText: scriptName }).first();
  await versionRowScript.getByRole("button", { name: "版本", exact: true }).click();
  const versionDialog = page.getByRole("dialog").filter({ hasText: `${scriptName} · 版本历史` }).last();
  await versionDialog.getByRole("heading", { name: `${scriptName} · 版本历史`, exact: true }).waitFor();
  await versionDialog.locator("tbody tr").filter({ hasText: "v1" }).waitFor();
  await versionDialog.locator("tbody tr").filter({ hasText: "v2" }).waitFor();
  await versionDialog.locator("tbody tr").filter({ hasText: "v3" }).waitFor();
  await versionDialog.locator("tbody tr").filter({ hasText: "v4" }).waitFor();
  assert.equal(await versionDialog.locator("tbody tr").count(), 4);
  await versionDialog.getByText("v4 · 当前", { exact: true }).waitFor();

  const v1Row = versionDialog.locator("tbody tr").filter({ hasText: "v1" }).first();
  await v1Row.getByRole("button", { name: "回滚", exact: true }).click();
  const rollbackDialog = page.getByRole("dialog").filter({ hasText: "回滚协议脚本" }).last();
  await rollbackDialog.getByRole("heading", { name: "回滚协议脚本", exact: true }).waitFor();
  const rollbackResponsePromise = page.waitForResponse((response) => isBrowserResponse(
    response,
    "POST",
    `/api/v1/acquisition/scripts/${script.id}/rollback`,
  ));
  await rollbackDialog.getByRole("button", { name: "回滚", exact: true }).click();
  const rollbackResponse = await rollbackResponsePromise;
  assert.equal(rollbackResponse.status(), 200);
  await page.getByText("已回滚到 v1", { exact: true }).waitFor();
  await versionDialog.getByText("v1 · 当前", { exact: true }).waitFor();
  const rolledBack = await apiJSON(`/api/v1/acquisition/scripts/${script.id}`, token);
  assert.equal(rolledBack.publishedVersion.versionNo, 1);
  const stateAfterRollback = await waitForScriptState(token, device.id, 1, "v1");
  assert.equal(stateAfterRollback.state.marker, "v1");
  await versionDialog.getByRole("button", { name: "关闭详情弹窗" }).click();

  await page.goto(`${webBase}/acquisition/device`, { waitUntil: "networkidle" });
  await page.getByRole("heading", { name: "设备管理", exact: true }).waitFor();
  await updateDeviceScript(page, device.id, deviceName, null);
  const unboundDevice = await apiJSON(`/api/v1/acquisition/devices/${device.id}`, token);
  assert.ok(unboundDevice.scriptId == null);

  console.log(
    "acquisition script API → browser E2E passed",
    JSON.stringify({ scriptID: script.id, deviceID: device.id, versions: [1, 2, 3, 4], runtimeVersion: 1, runtimeError: "SCRIPT_RUNTIME", runtimeLimit: "SCRIPT_LIMIT" }),
  );
} finally {
  if (browser) await browser.close();
  for (const child of children.reverse()) await stopProcess(child);
  await rm(tempRoot, { recursive: true, force: true });
}
