import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const port = 4183;
const baseUrl = `http://127.0.0.1:${port}`;
const vite = spawn(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(port)], {
  cwd: new URL("..", import.meta.url),
  stdio: ["ignore", "pipe", "pipe"],
});

const allPermissions = [
  "mqtt:overview:list",
  "mqtt:config:list",
  "mqtt:config:edit",
  "mqtt:config:test",
  "mqtt:command:list",
  "mqtt:command:detail",
  "mqtt:monitor:list",
];

const mqttConfig = {
  enabled: true,
  edgeId: "edge-01",
  brokerUrl: "mqtt://127.0.0.1:1883",
  protocolVersion: "MQTT_5",
  clientId: "edge-collector",
  username: "",
  passwordConfigured: false,
  tlsEnabled: false,
  caCertificate: "",
  clientCertificate: "",
  clientPrivateKeyConfigured: false,
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
};

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

async function mockApi(page) {
  await page.route("**/api/v1/mqtt/**", async (route) => {
    const requestUrl = new URL(route.request().url());
    const path = requestUrl.pathname;
    if (path.endsWith("/config")) {
      await route.fulfill({ contentType: "application/json", body: envelope(mqttConfig) });
      return;
    }
    if (path.endsWith("/state")) {
      await route.fulfill({ contentType: "application/json", body: envelope({
        state: "CONNECTED",
        connected: true,
        reconnectAttempts: 0,
        nextRetryAt: null,
        lastConnectedAt: "2026-09-18T00:00:00Z",
        lastDisconnectedAt: null,
        subscriptionFilter: "edge/edge-01/status",
        pendingLatestCount: 0,
        outbox: { rows: 0, bytes: 0, oldestAge: 0, maxRows: 100, maxBytes: 1024, rowUtilization: 0, byteUtilization: 0 },
      }) });
      return;
    }
    if (path.endsWith("/outbox/stats")) {
      await route.fulfill({ contentType: "application/json", body: envelope({
        rows: 0, bytes: 0, oldestAge: 0, lastError: null, maxRows: 100, maxBytes: 1024, rowUtilization: 0, byteUtilization: 0,
      }) });
      return;
    }
    if (path.endsWith("/commands")) {
      await route.fulfill({ contentType: "application/json", body: envelope({ records: [], total: 0, page: 1, pageSize: 20 }) });
      return;
    }
    await route.continue();
  });
}

async function openPage(browser, { permissions, route = "/mqtt" }) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error));
  await mockApi(page);
  const query = new URLSearchParams();
  if (permissions !== undefined) query.set("permissions", permissions);
  query.set("route", route);
  await page.goto(`${baseUrl}/tests/mqtt-admin.html?${query.toString()}`, { waitUntil: "networkidle" });
  return { page, errors };
}

async function expectRedirect(browser, permissionCodes, expectedRoute) {
  const { page, errors } = await openPage(browser, { permissions: permissionCodes.join(","), route: "/mqtt" });
  try {
    await page.getByTestId(`route-${expectedRoute}`).waitFor({ timeout: 3000 });
    await page.waitForTimeout(100);
    assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
  } finally {
    await page.close();
  }
}

async function expectGuard(browser, { permissions, route, text }) {
  const { page, errors } = await openPage(browser, { permissions, route });
  try {
    await page.getByText(text, { exact: true }).waitFor({ timeout: 3000 });
    assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
  } finally {
    await page.close();
  }
}

try {
  await waitForServer();
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}),
  });
  try {
    await expectRedirect(browser, allPermissions, "overview");
    await expectRedirect(browser, ["mqtt:config:list"], "config");
    await expectRedirect(browser, ["mqtt:config:list", "mqtt:command:list"], "config");
    await expectRedirect(browser, ["mqtt:command:list"], "commands");
    await expectRedirect(browser, ["mqtt:command:list", "mqtt:monitor:list"], "commands");
    await expectRedirect(browser, ["mqtt:monitor:list"], "monitor");

    const noPermission = await openPage(browser, { permissions: "none", route: "/mqtt" });
    try {
      await noPermission.page.getByTestId("route-overview").waitFor();
      await noPermission.page.getByText("无权查看运行总览", { exact: true }).waitFor();
      assert.equal(await noPermission.page.locator('[data-nav-path="/mqtt"]').count(), 0, "MQTT 目录不应在无权限时显示");
      assert.equal(await noPermission.page.locator('[data-nav-path^="/mqtt/"]').count(), 0, "MQTT 子页面不应在无权限时显示");
      assert.equal(noPermission.errors.length, 0, noPermission.errors.map((error) => error.message).join("\n"));
    } finally {
      await noPermission.page.close();
    }

    await expectGuard(browser, { permissions: "none", route: "/mqtt/config", text: "无权查看连接配置" });
    await expectGuard(browser, { permissions: "none", route: "/mqtt/commands", text: "无权查看 Command Journal" });
    await expectGuard(browser, { permissions: "none", route: "/mqtt/monitor", text: "无权查看 MQTT 消息监控" });

    const commandOnly = await openPage(browser, { permissions: "mqtt:command:list", route: "/mqtt" });
    try {
      await commandOnly.page.getByTestId("route-commands").waitFor();
      assert.equal(await commandOnly.page.locator('[data-nav-path="/mqtt"]').count(), 1, "有 Journal 权限时应显示 MQTT 目录");
      assert.equal(await commandOnly.page.locator('[data-nav-path="/mqtt/commands"]').count(), 1);
      assert.equal(await commandOnly.page.locator('[data-nav-path="/mqtt/config"]').count(), 0, "无配置权限时不应显示连接配置导航");
      assert.equal(commandOnly.errors.length, 0, commandOnly.errors.map((error) => error.message).join("\n"));
    } finally {
      await commandOnly.page.close();
    }

    const fullPermission = await openPage(browser, { permissions: allPermissions.join(","), route: "/mqtt/overview" });
    try {
      await fullPermission.page.getByTestId("route-overview").waitFor();
      for (const path of ["/mqtt", "/mqtt/overview", "/mqtt/config", "/mqtt/commands", "/mqtt/monitor"]) {
        assert.equal(await fullPermission.page.locator(`[data-nav-path="${path}"]`).count(), 1, `缺少导航项 ${path}`);
      }
      assert.equal(fullPermission.errors.length, 0, fullPermission.errors.map((error) => error.message).join("\n"));
    } finally {
      await fullPermission.page.close();
    }

    console.log("mqtt routes and permission guards passed");
  } finally {
    await browser.close();
  }
} finally {
  vite.kill("SIGTERM");
}
