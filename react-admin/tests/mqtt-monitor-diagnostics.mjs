import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const port = 4187;
const baseUrl = `http://127.0.0.1:${port}`;
const vite = spawn(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(port)], {
  cwd: new URL("..", import.meta.url),
  stdio: ["ignore", "pipe", "pipe"],
});

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

function payload(envelope) {
  return JSON.stringify(envelope);
}

async function openMonitor(browser) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error));
  const query = new URLSearchParams({ permissions: "mqtt:monitor:list", route: "/mqtt/monitor" });
  await page.goto(`${baseUrl}/tests/mqtt-admin.html?${query.toString()}`, { waitUntil: "networkidle" });
  await page.getByTestId("mqtt-monitor-page").waitFor();
  return { page, errors };
}

async function connect(page) {
  await page.getByLabel("密码", { exact: true }).fill("broker-secret");
  await page.getByRole("button", { name: "连接 Broker", exact: true }).click();
  await page.evaluate(() => window.mqttAdminTest.emitConnect());
  await page.getByRole("status", { name: "MQTT 连接状态：已连接" }).waitFor();
}

async function runCase(name, callback) {
  try {
    await callback();
    console.log(`mqtt monitor diagnostics: ${name} passed`);
  } catch (error) {
    console.error(`mqtt monitor diagnostics: ${name} failed`);
    throw error;
  }
}

const baseEnvelope = {
  messageId: "message-1",
  timestamp: "2026-09-18T16:30:00Z",
};

try {
  await waitForServer();
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}),
  });
  try {
    await runCase("five v1 schemas, unknown schema, identity mismatch and time fallbacks", async () => {
      const { page, errors } = await openMonitor(browser);
      try {
        await connect(page);
        const list = page.getByTestId("mqtt-monitor-message-list");
        const emit = (topic, envelope, packet) => page.evaluate(
          ({ topic, body, packet }) => window.mqttAdminTest.emitMessage(topic, body, packet),
          { topic, body: payload(envelope), packet },
        );

        const known = [
          ["edge/edge-01/status", { schema: "edge-status/v1", edgeId: "edge-01" }, "Edge 状态"],
          ["edge/edge-01/device/device-01/status", { schema: "device-status/v1", edgeId: "edge-01", deviceId: "device-01" }, "设备状态"],
          ["edge/edge-01/device/device-01/raw", { schema: "raw-register-snapshot/v1", edgeId: "edge-01", deviceId: "device-01" }, "Raw Snapshot"],
          ["edge/edge-01/device/device-01/event", { schema: "device-event/v1", edgeId: "edge-01", deviceId: "device-01" }, "设备事件"],
          ["edge/edge-01/device/device-01/command-result", { schema: "device-command-result/v1", edgeId: "edge-01", deviceId: "device-01" }, "Command Result"],
        ];
        for (const [index, [topic, envelope, label]] of known.entries()) {
          await emit(topic, { ...baseEnvelope, messageId: `known-${index}`, ...envelope }, { qos: 1 });
          await list.getByText(label, { exact: true }).waitFor();
        }
        assert.equal(await list.getByText("正常", { exact: true }).count(), 5, "五类 v1 Schema 应全部识别为正常");

        await emit("edge/edge-01/device/device-02/event", {
          ...baseEnvelope,
          messageId: "unknown-1",
          schema: "device-unknown/v9",
          edgeId: "edge-01",
          deviceId: "device-02",
        });
        await list.getByText("未知 Schema", { exact: true }).waitFor();
        assert.equal(await list.getByText("device-02", { exact: true }).count(), 1, "未知 Schema 消息仍应保留在列表");

        await emit("edge/edge-01/device/device-03/event", {
          ...baseEnvelope,
          messageId: "mismatch-1",
          schema: "device-event/v1",
          edgeId: "edge-01",
          deviceId: "device-99",
        });
        await list.getByText("标识不一致", { exact: true }).waitFor();

        await emit("edge/edge-01/device/device-04/event", {
          ...baseEnvelope,
          messageId: "bad-time",
          schema: "device-event/v1",
          edgeId: "edge-01",
          deviceId: "device-04",
          timestamp: "not-a-date",
        });
        const invalidRow = list.locator("tbody tr").filter({ hasText: "device-04" });
        await invalidRow.getByText("时间非法", { exact: true }).waitFor();
        assert.equal(await invalidRow.getByText("-", { exact: true }).count(), 1, "非法 Payload 时间应回退为 -");

        await emit("edge/edge-01/device/device-05/event", {
          messageId: "missing-time",
          schema: "device-event/v1",
          edgeId: "edge-01",
          deviceId: "device-05",
        });
        const missingRow = list.locator("tbody tr").filter({ hasText: "device-05" });
        await missingRow.getByText("正常", { exact: true }).waitFor();
        assert.equal(await missingRow.getByText("-", { exact: true }).count(), 1, "缺失 Payload 时间应回退为 -");

        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("terminal authentication error stops reconnect", async () => {
      const { page, errors } = await openMonitor(browser);
      try {
        await connect(page);
        await page.evaluate(() => window.mqttAdminTest.emitError("Not authorized: bad user name or password"));
        await page.getByRole("status", { name: "MQTT 连接状态：连接错误" }).waitFor();
        await page.getByRole("alert").getByText(/Not authorized/).waitFor();
        const state = await page.evaluate(() => ({
          clientCount: window.mqttAdminTest.clients.length,
          ended: window.mqttAdminTest.clients.at(-1)?.ended,
          force: window.mqttAdminTest.clients.at(-1)?.endForce,
        }));
        assert.equal(state.clientCount, 1);
        assert.equal(state.ended, true, "终端认证错误应主动结束客户端");
        assert.equal(state.force, true);

        await page.evaluate(() => window.mqttAdminTest.emitClose());
        await delay(200);
        await page.getByRole("status", { name: "MQTT 连接状态：连接错误" }).waitFor();
        assert.equal(await page.evaluate(() => window.mqttAdminTest.clients.length), 1, "终端错误后不应创建新客户端重连");
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("temporary transport error keeps reconnecting", async () => {
      const { page, errors } = await openMonitor(browser);
      try {
        await connect(page);
        await page.evaluate(() => window.mqttAdminTest.emitError("connection lost"));
        await page.evaluate(() => window.mqttAdminTest.emitClose());
        await page.getByRole("status", { name: "MQTT 连接状态：重连中" }).waitFor();
        const state = await page.evaluate(() => ({
          clientCount: window.mqttAdminTest.clients.length,
          ended: window.mqttAdminTest.clients.at(-1)?.ended,
        }));
        assert.equal(state.clientCount, 1, "临时传输失败不应创建新的客户端实例");
        assert.equal(state.ended, false, "临时传输失败不应主动结束客户端");

        await page.evaluate(() => window.mqttAdminTest.emitReconnect());
        await page.getByRole("status", { name: "MQTT 连接状态：重连中" }).waitFor();
        await page.evaluate(() => window.mqttAdminTest.emitConnect());
        await page.getByRole("status", { name: "MQTT 连接状态：已连接" }).waitFor();
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    console.log("mqtt monitor diagnostics browser behavior passed");
  } finally {
    await browser.close();
  }
} finally {
  vite.kill("SIGTERM");
}
