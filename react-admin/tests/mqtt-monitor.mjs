import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const port = 4181;
const baseUrl = `http://127.0.0.1:${port}`;
const vite = spawn(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(port)], {
  cwd: new URL("..", import.meta.url),
  stdio: ["ignore", "pipe", "pipe"],
});

async function waitForServer() {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const response = await fetch(`${baseUrl}/tests/mqtt-monitor.html`);
      if (response.ok) return;
    } catch {
      // Vite is still starting.
    }
    await delay(250);
  }
  throw new Error("Vite test server did not start");
}

try {
  await waitForServer();
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}),
  });
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
    const pageErrors = [];
    page.on("pageerror", (error) => pageErrors.push(error));
    page.on("console", (message) => {
      if (message.type() === "error") console.error(`browser console: ${message.text()}`);
    });
    await page.goto(`${baseUrl}/tests/mqtt-monitor.html`, { waitUntil: "networkidle" });
    if (pageErrors.length > 0) throw new Error(pageErrors.map((error) => error.stack ?? error.message).join("\n"));
    await page.getByTestId("mqtt-monitor-page").waitFor();
    await page.getByLabel("密码", { exact: true }).fill("broker-secret");
    await page.getByRole("button", { name: "连接 Broker", exact: true }).click();
    await page.evaluate(() => window.mqttMonitorTest.connect());
    await page.getByRole("status", { name: "MQTT 连接状态：已连接" }).waitFor();

    const subscriptions = await page.evaluate(() => window.mqttMonitorTest.clients.at(-1)?.subscriptions ?? []);
    assert.deepEqual(subscriptions.sort(), [
      "edge/edge-01/device/+/command-result",
      "edge/edge-01/device/+/event",
      "edge/edge-01/device/+/raw",
      "edge/edge-01/device/+/status",
      "edge/edge-01/status",
    ].sort());

    await page.evaluate(() => window.mqttMonitorTest.emitMessage(
      "edge/edge-01/device/device-01/raw",
      JSON.stringify({
        schema: "raw-register-snapshot/v1",
        messageId: "message-1",
        edgeId: "edge-01",
        deviceId: "device-01",
        timestamp: "2026-09-18T00:00:00Z",
        data: { blocks: [] },
      }),
      { qos: 0, retain: true },
    ));
    const messageList = page.getByTestId("mqtt-monitor-message-list");
    await messageList.getByText("Raw Snapshot", { exact: true }).waitFor();
    await messageList.getByText("device-01", { exact: true }).waitFor();
    await messageList.getByText("Retain", { exact: false }).waitFor();

    await page.evaluate(() => window.mqttMonitorTest.emitMessage("edge/edge-01/device/device-01/event", "not-json", { qos: 1, dup: true }));
    await page.getByText("JSON 解析失败", { exact: true }).waitFor();
    await page.getByLabel("仅异常").check();
    assert.equal(await page.getByText("JSON 解析失败", { exact: true }).count(), 1);
    await page.getByLabel("仅异常").uncheck();

    await page.getByRole("button", { name: "暂停显示", exact: true }).click();
    await page.evaluate(() => window.mqttMonitorTest.emitMessage("edge/edge-01/device/device-01/status", JSON.stringify({ schema: "device-status/v1", messageId: "message-2", edgeId: "edge-01", deviceId: "device-01", timestamp: "2026-09-18T00:00:00Z", data: {} })));
    await page.getByText("跳过 1", { exact: true }).waitFor();
    await page.getByRole("button", { name: "继续接收", exact: true }).click();
    await page.getByRole("button", { name: "查看", exact: true }).first().click();
    await page.getByRole("dialog").getByText("Payload", { exact: true }).waitFor();
    await page.getByRole("button", { name: "关闭详情弹窗", exact: true }).click();

    const stored = await page.evaluate(() => sessionStorage.getItem("mqtt-monitor-settings") ?? "");
    assert.ok(stored.includes("wsUrl"));
    assert.equal(stored.includes("broker-secret"), false);

    const limits = await page.evaluate(async () => {
      const { appendMonitorMessage, createEmptyMonitorBuffer, parseMonitorMessage } = await import("/src/lib/mqtt-monitor.ts");
      const settings = { topicPrefix: "edge", edgeId: "edge-01" };
      const makePayload = (index) => new TextEncoder().encode(JSON.stringify({
        schema: "device-event/v1",
        messageId: `event-${index}`,
        edgeId: "edge-01",
        deviceId: "device-01",
        timestamp: "2026-09-18T00:00:00Z",
        data: { padding: "x".repeat(24000) },
      }));
      let buffer = createEmptyMonitorBuffer();
      for (let index = 0; index < 600; index += 1) {
        buffer = appendMonitorMessage(buffer, parseMonitorMessage({
          topic: "edge/edge-01/device/device-01/event",
          payload: makePayload(index),
          settings,
          id: `message-${index}`,
        }));
      }
      const oversized = parseMonitorMessage({
        topic: "edge/edge-01/device/device-01/event",
        payload: new Uint8Array(1024 * 1024 + 1),
        settings,
      });
      buffer = appendMonitorMessage(buffer, oversized);
      return {
        count: buffer.messages.length,
        bytes: buffer.stats.bytes,
        evicted: buffer.stats.evicted,
        oversized: buffer.stats.oversized,
        oversizedDiagnostic: oversized.diagnostic,
      };
    });
    assert.ok(limits.count <= 500);
    assert.ok(limits.bytes <= 10 * 1024 * 1024);
    assert.ok(limits.evicted > 0);
    assert.equal(limits.oversized, 1);
    assert.equal(limits.oversizedDiagnostic, "oversized");
    assert.equal(pageErrors.length, 0, pageErrors.map((error) => error.message).join("\n"));
    console.log("mqtt monitor browser behavior passed");
  } finally {
    await browser.close();
  }
} finally {
  vite.kill("SIGTERM");
}
