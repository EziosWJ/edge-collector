import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const port = 4185;
const baseUrl = `http://127.0.0.1:${port}`;
const vite = spawn(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(port)], {
  cwd: new URL("..", import.meta.url),
  stdio: ["ignore", "pipe", "pipe"],
});

function baseConfig(overrides = {}) {
  return {
    enabled: true,
    edgeId: "edge-01",
    brokerUrl: "mqtt://127.0.0.1:1883",
    protocolVersion: "MQTT_5",
    clientId: "edge-collector",
    username: "operator",
    passwordConfigured: true,
    tlsEnabled: true,
    caCertificate: "",
    clientCertificate: "",
    clientPrivateKeyConfigured: true,
    keepAliveSeconds: 30,
    connectTimeoutMs: 10000,
    reconnectMinMs: 1000,
    reconnectMaxMs: 60000,
    topicPrefix: "edge",
    rawPublishIntervalMs: 1000,
    outboxMaxRows: 10000,
    outboxMaxBytes: 67108864,
    outboxRetentionDays: 7,
    commandJournalRetentionDays: 7,
    commandJournalMaxRows: 10000,
    commandQueueCapacity: 32,
    commandPollFairness: 1,
    ...overrides,
  };
}

function envelope(data, code = 200, message = "success") {
  return JSON.stringify({ code, message, data });
}

async function waitForServer() {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const response = await fetch(`${baseUrl}/tests/mqtt-admin.html`);
      if (response.ok) return;
    } catch {
      // Vite is still starting.
    }
    await delay(250);
  }
  throw new Error("Vite test server did not start");
}

async function openConfig(browser, { permissions, config = baseConfig() }) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  const errors = [];
  const requests = [];
  let savedConfig = config;
  page.on("pageerror", (error) => errors.push(error));
  await page.route("**/api/v1/mqtt/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    if (path.endsWith("/config") && method === "GET") {
      await route.fulfill({ contentType: "application/json", body: envelope(savedConfig) });
      return;
    }
    if (path.endsWith("/config") && method === "PUT") {
      const body = request.postDataJSON();
      requests.push({ method, path, body });
      const next = baseConfig({
        ...savedConfig,
        ...body,
        passwordConfigured: body.passwordAction === "clear" ? false : savedConfig.passwordConfigured,
        clientPrivateKeyConfigured: body.clientPrivateKeyAction === "clear" ? false : savedConfig.clientPrivateKeyConfigured,
      });
      delete next.password;
      delete next.clientPrivateKey;
      delete next.passwordAction;
      delete next.clientPrivateKeyAction;
      savedConfig = next;
      await route.fulfill({ contentType: "application/json", body: envelope(savedConfig) });
      return;
    }
    if (path.endsWith("/test-connection") && method === "POST") {
      const raw = request.postData();
      requests.push({ method, path, body: raw ? JSON.parse(raw) : undefined });
      await route.fulfill({ contentType: "application/json", body: envelope({ success: true, runtimeConnected: true }) });
      return;
    }
    await route.continue();
  });
  const query = new URLSearchParams({ permissions, route: "/mqtt/config" });
  await page.goto(`${baseUrl}/tests/mqtt-admin.html?${query.toString()}`, { waitUntil: "networkidle" });
  return { page, errors, requests };
}

const editablePermissions = "mqtt:config:list,mqtt:config:edit,mqtt:config:test";

async function runCase(name, callback) {
  try {
    await callback();
    console.log(`mqtt config page: ${name} passed`);
  } catch (error) {
    console.error(`mqtt config page: ${name} failed`);
    throw error;
  }
}

try {
  await waitForServer();
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}),
  });
  try {
    await runCase("unsaved changes block navigation", async () => {
      const { page, errors, requests } = await openConfig(browser, { permissions: editablePermissions });
      try {
        const edgeInput = page.locator("#mqtt-edge-id");
        await edgeInput.waitFor();
        assert.equal(await edgeInput.inputValue(), "edge-01");
        await edgeInput.fill("edge-draft");
        await page.getByText("有未保存的配置变更", { exact: true }).waitFor();

        const messages = [];
        const [cancelDialog] = await Promise.all([
          page.waitForEvent("dialog"),
          page.getByTestId("go-elsewhere").click(),
        ]);
        messages.push(cancelDialog.message());
        await cancelDialog.dismiss();
        await page.getByTestId("route-config").waitFor();
        await page.getByText("有未保存的配置变更", { exact: true }).waitFor();
        assert.equal(await page.getByTestId("route-elsewhere").count(), 0);
        assert.match(messages[0] ?? "", /未保存变更/);

        const [leaveDialog] = await Promise.all([
          page.waitForEvent("dialog"),
          page.getByTestId("go-elsewhere").click(),
        ]);
        messages.push(leaveDialog.message());
        await leaveDialog.accept();
        await page.getByTestId("route-elsewhere").waitFor();
        assert.equal(messages.length, 2);
        assert.equal(requests.filter((item) => item.method === "PUT").length, 0, "离开页面不应保存配置");
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("draft connection test does not persist", async () => {
      const { page, errors, requests } = await openConfig(browser, { permissions: editablePermissions });
      try {
        await page.locator("#mqtt-edge-id").waitFor();
        await page.getByRole("button", { name: "测试连接", exact: true }).click();
        await page.locator("#root").getByText("Broker 连接测试成功，正式 runtime 已连接", { exact: true }).waitFor();
        const first = requests.find((item) => item.path.endsWith("/test-connection"));
        assert.ok(first, "测试连接应发起请求");
        assert.equal(first.body, undefined, "无草稿变更时应测试已保存配置");

        await page.locator("#mqtt-broker-url").fill("mqtt://draft.example:1883");
        await page.getByRole("button", { name: "测试连接", exact: true }).click();
        await page.locator("#root").getByText("Broker 连接测试成功，正式 runtime 已连接", { exact: true }).waitFor();
        const tests = requests.filter((item) => item.path.endsWith("/test-connection"));
        assert.equal(tests.length, 2);
        assert.equal(tests[1].body.brokerUrl, "mqtt://draft.example:1883", "草稿测试应携带草稿 Broker 地址");
        assert.equal(tests[1].body.passwordAction, "keep");
        assert.equal(Object.hasOwn(tests[1].body, "password"), false, "草稿测试不应回传 secret");
        assert.equal(requests.filter((item) => item.method === "PUT").length, 0, "草稿测试不应保存配置");
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("discard changes restores saved config", async () => {
      const { page, errors, requests } = await openConfig(browser, { permissions: editablePermissions });
      try {
        const edgeInput = page.locator("#mqtt-edge-id");
        await edgeInput.waitFor();
        await edgeInput.fill("edge-draft");
        await page.getByText("有未保存的配置变更", { exact: true }).waitFor();
        await page.getByRole("button", { name: "放弃变更", exact: true }).click();
        await page.getByText("有未保存的配置变更", { exact: true }).waitFor({ state: "detached" });
        assert.equal(await edgeInput.inputValue(), "edge-01");
        assert.equal(requests.filter((item) => item.method === "PUT").length, 0);
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("clearing password requires confirmation", async () => {
      const { page, errors, requests } = await openConfig(browser, { permissions: editablePermissions });
      try {
        await page.locator("#mqtt-edge-id").waitFor();
        await page.getByLabel("Broker 密码操作", { exact: true }).selectOption("clear");
        await page.getByText("有未保存的配置变更", { exact: true }).waitFor();

        const messages = [];
        const cancelDialogPromise = page.waitForEvent("dialog");
        const cancelClick = page.getByRole("button", { name: "保存配置", exact: true }).click().catch(() => {});
        const cancelDialog = await cancelDialogPromise;
        messages.push(cancelDialog.message());
        await cancelDialog.dismiss();
        await cancelClick;
        await page.waitForTimeout(100);
        assert.equal(requests.filter((item) => item.method === "PUT").length, 0, "取消确认后不应保存");
        assert.match(messages[0] ?? "", /清除已配置的 secret/);

        const confirmDialogPromise = page.waitForEvent("dialog");
        const confirmClick = page.getByRole("button", { name: "保存配置", exact: true }).click().catch(() => {});
        const confirmDialog = await confirmDialogPromise;
        messages.push(confirmDialog.message());
        await confirmDialog.accept();
        await confirmClick;
        await page.getByText("MQTT 配置已保存，runtime 正在按新配置重连", { exact: true }).waitFor();
        const saves = requests.filter((item) => item.method === "PUT");
        assert.equal(saves.length, 1);
        assert.equal(saves[0].body.passwordAction, "clear");
        assert.equal(Object.hasOwn(saves[0].body, "password"), false, "清除 Secret 时请求体不应携带密码");
        assert.equal(messages.length, 2);
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("read-only permissions hide write and test actions", async () => {
      const { page, errors } = await openConfig(browser, { permissions: "mqtt:config:list" });
      try {
        await page.locator("#mqtt-edge-id").waitFor();
        assert.equal(await page.locator("#mqtt-edge-id").isDisabled(), true, "只读账号不能编辑配置");
        await page.getByText("当前账号无配置写入权限", { exact: true }).waitFor();
        assert.equal(await page.getByRole("button", { name: "测试连接", exact: true }).count(), 0, "无测试权限时应隐藏测试按钮");
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    console.log("mqtt config page browser behavior passed");
  } finally {
    await browser.close();
  }
} finally {
  vite.kill("SIGTERM");
}
