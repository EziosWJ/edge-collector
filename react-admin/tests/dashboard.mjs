import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const port = 4191;
const baseUrl = `http://127.0.0.1:${port}`;
const vite = spawn(
  process.execPath,
  ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(port)],
  {
    cwd: new URL("..", import.meta.url),
    stdio: ["ignore", "pipe", "pipe"],
    env: { ...process.env, VITE_API_BASE_URL: "" },
  },
);

const currentUser = {
  id: 1,
  username: "operator",
  nickname: "运维人员",
  roles: [{ id: 1, roleName: "运维管理员", roleCode: "OPS" }],
};

const healthyChannels = [
  {
    id: 1,
    name: "RTU 主通道",
    protocol: "MODBUS_RTU",
    timeoutMs: 1000,
    interRequestDelayMs: 50,
    enabled: 1,
  },
  {
    id: 2,
    name: "TCP 备用通道",
    protocol: "MODBUS_TCP",
    timeoutMs: 1000,
    interRequestDelayMs: 50,
    enabled: 1,
  },
];

const healthyDevices = [
  {
    id: 101,
    name: "馈线保护装置 A",
    deviceType: "FEED_PROTECTOR",
    channelId: 1,
    unitId: 1,
    pollIntervalMs: 1000,
    failureThreshold: 3,
    enabled: 1,
    registerBlocks: [],
  },
  {
    id: 201,
    name: "馈线保护装置 B",
    deviceType: "FEED_PROTECTOR",
    channelId: 2,
    unitId: 2,
    pollIntervalMs: 1000,
    failureThreshold: 3,
    enabled: 1,
    registerBlocks: [],
  },
];

function envelope(data, code = 200, message = "success") {
  return JSON.stringify({ code, message, data });
}

function pageResult(records) {
  return { records, total: records.length, page: 1, pageSize: 100 };
}

function stateFor(device, status = "ONLINE", lastError = null) {
  return {
    deviceId: device.id,
    deviceName: device.name,
    channelId: device.channelId,
    unitId: device.unitId,
    registerBlocks: [],
    lastAttemptAt: "2026-09-19T01:00:00.000Z",
    lastSuccessAt: status === "INITIAL" ? null : "2026-09-19T00:59:59.000Z",
    status,
    consecutiveFailures: status === "OFFLINE" ? 3 : 0,
    lastError,
  };
}

function channelState(channel, status = "ONLINE", lastError = null) {
  return {
    channelId: channel.id,
    channelName: channel.name,
    protocol: channel.protocol,
    status,
    lastAttemptAt: "2026-09-19T01:00:00.000Z",
    lastSuccessAt: status === "IDLE" ? null : "2026-09-19T00:59:59.000Z",
    lastError,
  };
}

function scenarioData(name) {
  const mqtt = {
    state: "CONNECTED",
    connected: true,
    reconnectAttempts: 0,
    nextRetryAt: null,
    lastConnectedAt: "2026-09-19T00:59:00.000Z",
    lastDisconnectedAt: null,
    subscriptionFilter: "edge/+/device/+/event",
    pendingLatestCount: 0,
  };
  const outbox = {
    rows: 10,
    bytes: 1024,
    oldestAge: 0,
    lastError: null,
    maxRows: 100,
    maxBytes: 10240,
    rowUtilization: 0.1,
    byteUtilization: 0.1,
  };

  if (name === "healthy") {
    return {
      channels: healthyChannels,
      devices: healthyDevices,
      channelStates: healthyChannels.map((channel) => channelState(channel)),
      states: healthyDevices.map((device) => stateFor(device)),
      scriptStates: [],
      mqtt,
      outbox,
      health: true,
    };
  }

  if (name === "aggregation") {
    return {
      channels: healthyChannels,
      devices: [
        ...healthyDevices,
        { ...healthyDevices[0], id: 102, name: "馈线保护装置 C" },
      ],
      channelStates: [
        channelState(healthyChannels[0], "OFFLINE", "RTU 超时"),
        channelState(healthyChannels[1]),
      ],
      states: [
        stateFor(healthyDevices[0], "OFFLINE", "设备无响应"),
        stateFor({ ...healthyDevices[0], id: 102, name: "馈线保护装置 C" }, "OFFLINE", "设备无响应"),
        stateFor(healthyDevices[1], "OFFLINE", "设备无响应"),
      ],
      scriptStates: [
        {
          deviceId: 201,
          deviceName: "馈线保护装置 B",
          scriptId: 9,
          scriptVersionId: 90,
          versionNo: 3,
          lastAttemptAt: "2026-09-19T01:00:00.000Z",
          lastSuccessAt: "2026-09-18T23:00:00.000Z",
          lastError: "脚本运行失败",
          lastErrorType: "SCRIPT_RUNTIME",
          state: {},
          events: [],
        },
      ],
      mqtt: { ...mqtt, state: "ERROR", connected: false, lastError: "Broker 连接失败" },
      outbox: { ...outbox, rows: 80, rowUtilization: 0.8 },
      health: true,
    };
  }

  if (name === "capacity") {
    return {
      channels: healthyChannels,
      devices: healthyDevices,
      channelStates: healthyChannels.map((channel) => channelState(channel)),
      states: healthyDevices.map((device) => stateFor(device)),
      scriptStates: [],
      mqtt,
      outbox: { ...outbox, rows: 80, rowUtilization: 0.8, bytes: 8192, byteUtilization: 0.8 },
      health: true,
    };
  }

  if (name === "unconfigured") {
    return {
      channels: [],
      devices: [],
      channelStates: [],
      states: [],
      scriptStates: [],
      mqtt,
      outbox,
      health: true,
    };
  }

  if (name === "initial") {
    return {
      channels: [healthyChannels[0]],
      devices: [healthyDevices[0]],
      channelStates: [channelState(healthyChannels[0], "STARTING")],
      states: [stateFor(healthyDevices[0], "INITIAL")],
      scriptStates: [],
      mqtt,
      outbox,
      health: true,
    };
  }

  return {
    channels: healthyChannels,
    devices: healthyDevices,
    channelStates: healthyChannels.map((channel) => channelState(channel)),
    states: healthyDevices.map((device) => stateFor(device)),
    scriptStates: [],
    mqtt,
    outbox,
    health: true,
  };
}

async function waitForServer() {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const response = await fetch(`${baseUrl}/`);
      if (response.ok) return;
    } catch {
      // Vite is still starting.
    }
    await delay(250);
  }
  throw new Error("Vite test server did not start");
}

async function mockApi(page, scenarioName, options = {}) {
  const data = scenarioData(scenarioName);
  let refreshCount = 0;
  let statesFailed = Boolean(options.failFirstStates);
  let firstRefreshFailed = Boolean(options.failOnRefresh);
  let mqttForbidden = Boolean(options.mqttForbidden);

  await page.route("**/*", async (route) => {
    const requestUrl = new URL(route.request().url());
    const path = requestUrl.pathname;

    if (path === "/api/auth/me") {
      await route.fulfill({ contentType: "application/json", body: envelope(currentUser) });
      return;
    }
    if (path === "/api/auth/menus") {
      await route.fulfill({ contentType: "application/json", body: envelope([]) });
      return;
    }
    if (path === "/health" || path === "/ready") {
      const ok = data.health;
      await route.fulfill({ status: ok ? 200 : 503, contentType: "application/json", body: envelope({ status: ok ? "ok" : "unavailable" }, ok ? 200 : 503, ok ? "success" : "service unavailable") });
      return;
    }
    if (!path.startsWith("/api/v1/")) {
      await route.continue();
      return;
    }

    if (path === "/api/v1/acquisition/states" && statesFailed) {
      statesFailed = false;
      await route.fulfill({ status: 500, contentType: "application/json", body: envelope(null, 500, "采集状态暂时不可用") });
      return;
    }

    if (path === "/api/v1/mqtt/state" || path === "/api/v1/mqtt/outbox/stats") {
      if (mqttForbidden) {
        await route.fulfill({ status: 403, contentType: "application/json", body: envelope(null, 403, "无权查看 MQTT 运行状态") });
        return;
      }
      if (path === "/api/v1/mqtt/state") {
        await route.fulfill({ contentType: "application/json", body: envelope(data.mqtt) });
        return;
      }
      const currentOutbox = { ...data.outbox };
      if (scenarioName === "capacity" && refreshCount > 0) {
        currentOutbox.rows = 100;
        currentOutbox.bytes = 10240;
        currentOutbox.rowUtilization = 1;
        currentOutbox.byteUtilization = 1;
        currentOutbox.oldestAge = 120;
      }
      await route.fulfill({ contentType: "application/json", body: envelope(currentOutbox) });
      return;
    }

    if (path === "/api/v1/acquisition/channels") {
      await route.fulfill({ contentType: "application/json", body: envelope(pageResult(data.channels)) });
      return;
    }
    if (path === "/api/v1/acquisition/devices") {
      await route.fulfill({ contentType: "application/json", body: envelope(pageResult(data.devices)) });
      return;
    }
    if (path === "/api/v1/acquisition/states") {
      if (firstRefreshFailed && refreshCount > 0) {
        await route.fulfill({ status: 503, contentType: "application/json", body: envelope(null, 503, "采集服务暂时不可用") });
        return;
      }
      await route.fulfill({ contentType: "application/json", body: envelope(data.states) });
      return;
    }
    if (path === "/api/v1/acquisition/channel-state") {
      await route.fulfill({ contentType: "application/json", body: envelope(data.channelStates) });
      return;
    }
    if (path === "/api/v1/acquisition/script-states") {
      await route.fulfill({ contentType: "application/json", body: envelope(data.scriptStates) });
      return;
    }

    await route.continue();
  });

  await page.addInitScript(({ user }) => {
    localStorage.setItem("react-admin-auth", JSON.stringify({ token: "dashboard-test-token", user }));
  }, { user: currentUser });

  return {
    markRefresh() {
      refreshCount += 1;
    },
  };
}

async function openDashboard(browser, scenarioName, options = {}) {
  const page = await browser.newPage({ viewport: options.viewport ?? { width: 1280, height: 900 } });
  const mock = await mockApi(page, scenarioName, options);
  await page.goto(`${baseUrl}/dashboard`, { waitUntil: "domcontentloaded" });
  return { page, mock };
}

async function waitForDashboard(page) {
  await page.getByTestId("dashboard-page").waitFor({ state: "visible", timeout: 3000 });
}

async function clickRefresh(page, mock) {
  mock.markRefresh();
  await page.getByTestId("dashboard-refresh").click();
}

async function runCase(name, callback, failures) {
  try {
    await callback();
    console.log(`dashboard: ${name} passed`);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    failures.push(`${name}: ${message}`);
    console.error(`dashboard: ${name} failed - ${message}`);
  }
}

try {
  await waitForServer();
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH }
      : {}),
  });
  const failures = [];
  try {
    await runCase("health and channel/device aggregation", async () => {
      const { page } = await openDashboard(browser, "aggregation");
      try {
        await waitForDashboard(page);
        await page.getByTestId("dashboard-health-summary").waitFor();
        await page.getByTestId("dashboard-domain-acquisition").getByText("有通道离线").waitFor();
        await page.getByTestId("dashboard-domain-device").getByText("有设备离线").waitFor();
        await page.getByTestId("dashboard-domain-mqtt").getByText(/连接错误|未连接/).waitFor();
        await page.getByTestId("dashboard-domain-service").getByText("服务就绪", { exact: true }).waitFor();
        await page.getByTestId("dashboard-channel-board").getByTestId("dashboard-channel-row").first().waitFor();

        const attention = page.getByTestId("dashboard-attention");
        await attention.getByText("RTU 主通道").waitFor();
        await attention.getByText("TCP 备用通道").waitFor({ state: "detached" });
        await attention.getByText("馈线保护装置 C").waitFor({ state: "detached" });
        const scriptAttention = attention.getByRole("link", { name: /设备“馈线保护装置 B”协议脚本失败/ });
        await scriptAttention.waitFor();
        await attention.getByRole("link", { name: /通道“RTU 主通道”已离线/ }).getAttribute("href").then((href) => assert.match(href ?? "", /^\/acquisition\/realtime/));
        await attention.getByRole("link", { name: /设备“馈线保护装置 B”已离线/ }).getAttribute("href").then((href) => assert.match(href ?? "", /^\/acquisition\/realtime/));
        await scriptAttention.getAttribute("href").then((href) => assert.equal(href, "/acquisition/script?deviceId=201"));
        await attention.getByRole("link", { name: /MQTT 连接错误/ }).getAttribute("href").then((href) => assert.equal(href, "/mqtt/overview"));
      } finally {
        await page.close();
      }
    }, failures);

    await runCase("MQTT 80% and 100% capacity semantics", async () => {
      const { page, mock } = await openDashboard(browser, "capacity");
      try {
        await waitForDashboard(page);
        await page.getByTestId("dashboard-domain-mqtt").getByText(/80%/).waitFor();
        await clickRefresh(page, mock);
        await page.getByTestId("dashboard-domain-mqtt").getByText(/100%/).waitFor();
        await page.getByTestId("dashboard-attention").getByText("Reliable Outbox 已满", { exact: true }).waitFor();
      } finally {
        await page.close();
      }
    }, failures);

    await runCase("no attention items", async () => {
      const { page } = await openDashboard(browser, "healthy");
      try {
        await waitForDashboard(page);
        await page.getByTestId("dashboard-attention").getByText(/当前没有需要关注/).waitFor();
      } finally {
        await page.close();
      }
    }, failures);

    await runCase("unconfigured and initial acquisition states", async () => {
      const unconfigured = await openDashboard(browser, "unconfigured");
      try {
        await waitForDashboard(unconfigured.page);
        await unconfigured.page.getByTestId("dashboard-domain-acquisition").getByText("未配置", { exact: true }).waitFor();
        await unconfigured.page.getByTestId("dashboard-attention").getByText(/当前没有需要关注/).waitFor();
      } finally {
        await unconfigured.page.close();
      }

      const initial = await openDashboard(browser, "initial");
      try {
        await waitForDashboard(initial.page);
        await initial.page.getByText("等待首次采集").waitFor();
        await initial.page.getByTestId("dashboard-attention").getByText(/当前没有需要关注/).waitFor();
      } finally {
        await initial.page.close();
      }
    }, failures);

    await runCase("initial request failure and retry", async () => {
      const { page, mock } = await openDashboard(browser, "healthy", { failFirstStates: true });
      try {
        await waitForDashboard(page);
        await page.getByText(/加载失败|暂时不可用/).waitFor();
        await clickRefresh(page, mock);
        await page.getByTestId("dashboard-channel-board").getByText("RTU 主通道").waitFor();
      } finally {
        await page.close();
      }
    }, failures);

    await runCase("refresh failure preserves stale data", async () => {
      const { page, mock } = await openDashboard(browser, "healthy", { failOnRefresh: true });
      try {
        await waitForDashboard(page);
        await page.getByTestId("dashboard-channel-board").getByText("RTU 主通道").waitFor();
        await clickRefresh(page, mock);
        await page.getByRole("status").getByText("部分数据可能已过期；页面会在可见时继续尝试刷新。", { exact: true }).waitFor();
        await page.getByTestId("dashboard-channel-board").getByText("RTU 主通道").waitFor();
      } finally {
        await page.close();
      }
    }, failures);

    await runCase("MQTT partial failure stays visible", async () => {
      const { page } = await openDashboard(browser, "healthy", { mqttForbidden: true });
      try {
        await waitForDashboard(page);
        await page.getByTestId("dashboard-domain-mqtt").getByText("数据不可用").waitFor();
        await page.getByTestId("dashboard-attention").getByText("MQTT 运行数据暂不可用").waitFor();
      } finally {
        await page.close();
      }
    }, failures);

    await runCase("mobile layout and troubleshooting links", async () => {
      const { page } = await openDashboard(browser, "aggregation", { viewport: { width: 375, height: 800 } });
      try {
        await waitForDashboard(page);
        const links = await page.getByTestId("dashboard-attention").getByRole("link").evaluateAll((items) => items.map((item) => item.getAttribute("href")));
        assert.ok(links.some((href) => href?.startsWith("/acquisition/realtime")), `missing realtime troubleshooting link: ${links.join(", ")}`);
        assert.ok(links.includes("/acquisition/script"), `missing script troubleshooting link: ${links.join(", ")}`);
        assert.ok(links.includes("/mqtt/overview"), `missing MQTT troubleshooting link: ${links.join(", ")}`);
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
        assert.equal(overflow, false, "mobile Dashboard horizontally overflows");
      } finally {
        await page.close();
      }
    }, failures);
  } finally {
    await browser.close();
  }

  if (failures.length > 0) {
    throw new Error(`Dashboard browser contract failures:\n${failures.map((failure) => `- ${failure}`).join("\n")}`);
  }
  console.log("dashboard browser behavior passed");
} finally {
  vite.kill("SIGTERM");
}
