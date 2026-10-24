import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const port = 4184;
const baseUrl = `http://127.0.0.1:${port}`;
const vite = spawn(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(port)], {
  cwd: new URL("..", import.meta.url),
  stdio: ["ignore", "pipe", "pipe"],
});

const runtimeState = {
  state: "CONNECTED",
  connected: true,
  reconnectAttempts: 2,
  nextRetryAt: null,
  lastConnectedAt: "2026-09-18T08:00:00Z",
  lastDisconnectedAt: "2026-09-18T07:59:00Z",
  subscriptionFilter: "edge/edge-01/device/+/event",
  pendingLatestCount: 3,
  outbox: { rows: 80, bytes: 1024, oldestAge: 15, maxRows: 100, maxBytes: 1024, rowUtilization: 0.8, byteUtilization: 1 },
};

const outboxStats = {
  rows: 80,
  bytes: 1024,
  oldestAge: 15,
  lastError: null,
  maxRows: 100,
  maxBytes: 1024,
  rowUtilization: 0.8,
  byteUtilization: 1,
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

function createMock() {
  const mode = {
    state: "success",
    outbox: "success",
    stateDelay: 0,
    outboxDelay: 0,
  };
  const counts = { state: 0, outbox: 0 };
  return { mode, counts };
}

async function openOverview(browser, mock) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error));
  await page.route("**/api/v1/mqtt/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/state")) {
      mock.counts.state += 1;
      if (mock.mode.stateDelay) await delay(mock.mode.stateDelay);
      if (mock.mode.state === "fail") {
        await route.fulfill({ status: 503, contentType: "application/json", body: envelope(null, 503, "MQTT runtime 暂时不可用") });
        return;
      }
      await route.fulfill({ contentType: "application/json", body: envelope(runtimeState) });
      return;
    }
    if (path.endsWith("/outbox/stats")) {
      mock.counts.outbox += 1;
      if (mock.mode.outboxDelay) await delay(mock.mode.outboxDelay);
      if (mock.mode.outbox === "fail") {
        await route.fulfill({ status: 503, contentType: "application/json", body: envelope(null, 503, "Outbox 统计暂时不可用") });
        return;
      }
      await route.fulfill({ contentType: "application/json", body: envelope(outboxStats) });
      return;
    }
    if (path.endsWith("/config")) {
      await route.fulfill({ contentType: "application/json", body: envelope({}) });
      return;
    }
    await route.continue();
  });
  await page.goto(`${baseUrl}/tests/mqtt-admin.html?permissions=${encodeURIComponent("mqtt:overview:list")}&route=%2Fmqtt%2Foverview`, { waitUntil: "domcontentloaded" });
  return { page, errors };
}

async function runCase(name, callback) {
  try {
    await callback();
    console.log(`mqtt overview: ${name} passed`);
  } catch (error) {
    console.error(`mqtt overview: ${name} failed`);
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
    await runCase("runtime and outbox load independently", async () => {
      const mock = createMock();
      mock.mode.outboxDelay = 400;
      const { page, errors } = await openOverview(browser, mock);
      try {
        await page.getByText("Broker 已连接", { exact: true }).waitFor();
        await page.getByText("加载中", { exact: true }).waitFor();
        await page.getByText("80 条 · 1.0 KiB", { exact: true }).first().waitFor();
        assert.equal(mock.counts.state, 1);
        assert.equal(mock.counts.outbox, 1);
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("runtime failure keeps outbox visible and retries", async () => {
      const mock = createMock();
      mock.mode.state = "fail";
      const { page, errors } = await openOverview(browser, mock);
      try {
        await page.getByText("运行状态加载失败", { exact: true }).waitFor();
        await page.getByText("Reliable Outbox 容量", { exact: true }).waitFor();
        await page.getByText(/80 \/ 100 · 80%/).waitFor();
        mock.mode.state = "success";
        await page.getByRole("alert").getByRole("button", { name: "重试", exact: true }).click();
        await page.getByText("Broker 已连接", { exact: true }).waitFor();
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("outbox failure keeps runtime visible and retries", async () => {
      const mock = createMock();
      mock.mode.outbox = "fail";
      const { page, errors } = await openOverview(browser, mock);
      try {
        await page.getByText("Broker 已连接", { exact: true }).waitFor();
        await page.getByText("加载失败", { exact: true }).first().waitFor();
        await page.getByRole("alert").getByText("Reliable Outbox 区块加载失败：Outbox 统计暂时不可用", { exact: true }).waitFor();
        assert.equal(await page.getByRole("link", { name: /连接配置/ }).getAttribute("href"), "/mqtt/config#broker");
        assert.equal(await page.getByRole("link", { name: /Command Journal/ }).getAttribute("href"), "/mqtt/commands");
        mock.mode.outbox = "success";
        await page.getByRole("alert").getByRole("button", { name: "重试", exact: true }).click();
        await page.getByText("Reliable Outbox 容量", { exact: true }).waitFor();
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("refresh failure preserves data and marks it stale", async () => {
      const mock = createMock();
      const { page, errors } = await openOverview(browser, mock);
      try {
        await page.getByText("Broker 已连接", { exact: true }).waitFor();
        await page.getByText("Reliable Outbox 容量", { exact: true }).waitFor();
        const before = { state: mock.counts.state, outbox: mock.counts.outbox };
        mock.mode.state = "fail";
        mock.mode.outbox = "fail";
        await page.getByRole("button", { name: "刷新", exact: true }).click();
        await page.getByRole("status").getByText(/数据可能已过期；最近成功刷新：\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/).waitFor();
        assert.ok(mock.counts.state > before.state);
        assert.ok(mock.counts.outbox > before.outbox);
        await page.getByText("80 条 · 1.0 KiB", { exact: true }).first().waitFor();
        await page.getByText("Broker 已连接", { exact: true }).waitFor();
        await page.getByRole("alert").getByText("MQTT runtime 暂时不可用", { exact: true }).waitFor();
        await page.getByRole("alert").getByText(/Reliable Outbox 区块加载失败/).waitFor();
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("narrow layout has no horizontal overflow", async () => {
      const mock = createMock();
      const { page, errors } = await openOverview(browser, mock);
      try {
        await page.setViewportSize({ width: 375, height: 800 });
        await page.getByText("Broker 已连接", { exact: true }).waitFor();
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
        assert.equal(overflow, false, "窄屏运行总览出现横向溢出");
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    console.log("mqtt overview browser behavior passed");
  } finally {
    await browser.close();
  }
} finally {
  vite.kill("SIGTERM");
}
