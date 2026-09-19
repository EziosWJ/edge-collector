import assert from "node:assert/strict";
import http from "node:http";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { setTimeout as delay } from "node:timers/promises";
import { WebSocketServer } from "ws";
import { chromium } from "playwright";

const vitePort = 4182;
const viteBaseUrl = `http://127.0.0.1:${vitePort}`;
const httpServer = http.createServer();
const broker = new WebSocketServer({ server: httpServer });
let connectionCount = 0;
let closeCount = 0;

broker.on("connection", (socket) => {
  connectionCount += 1;
  socket.on("close", () => {
    closeCount += 1;
  });
  socket.on("message", (payload) => {
    if ((payload[0] >> 4) === 1) socket.send(Buffer.from([0x20, 0x03, 0x00, 0x00, 0x00]));
  });
});

httpServer.listen(0, "127.0.0.1");
await once(httpServer, "listening");
const brokerPort = httpServer.address().port;
const vite = spawn(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(vitePort)], {
  cwd: new URL("..", import.meta.url),
  stdio: ["ignore", "pipe", "pipe"],
});

async function waitForVite() {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const response = await fetch(`${viteBaseUrl}/tests/mqtt-monitor-live.html`);
      if (response.ok) return;
    } catch {
      // Vite is still starting.
    }
    await delay(250);
  }
  throw new Error("Vite test server did not start");
}

try {
  await waitForVite();
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}),
  });
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
    page.setDefaultTimeout(5000);
    await page.goto(`${viteBaseUrl}/tests/mqtt-monitor-live.html`, { waitUntil: "networkidle" });
    await page.getByTestId("mqtt-monitor-page").waitFor();
    await page.locator("#mqtt-monitor-url").fill(`ws://127.0.0.1:${brokerPort}`);
    await page.getByRole("button", { name: "连接 Broker", exact: true }).click();
    await page.getByRole("status", { name: "MQTT 连接状态：已连接" }).waitFor();
    assert.equal(connectionCount, 1);

    const disconnectButton = page.getByRole("button", { name: "断开", exact: true });
    const disconnectButtonType = await disconnectButton.getAttribute("type");
    await page.locator("form").evaluate((form) => {
      Object.assign(window, { mqttMonitorDisconnectSubmitCount: 0 });
      form.addEventListener("submit", () => {
        window.mqttMonitorDisconnectSubmitCount += 1;
      });
    });
    await disconnectButton.click();
    await delay(2500);
    const statusLabels = await page.locator("[role=status]").evaluateAll((nodes) => nodes.map((node) => node.getAttribute("aria-label") ?? node.textContent));
    const createdClientCount = await page.evaluate(() => window.mqttMonitorLiveTest.createdClientCount());
    const formSubmitCount = await page.evaluate(() => window.mqttMonitorDisconnectSubmitCount);
    const state = { connectionCount, closeCount, createdClientCount, disconnectButtonType, formSubmitCount, statusLabels };
    assert.equal(formSubmitCount, 0, `断开按钮不应提交表单：${JSON.stringify(state)}`);
    assert.ok(statusLabels.includes("MQTT 连接状态：未连接"), JSON.stringify(state));
    assert.equal(connectionCount, 1, `手动断开后不应重新建立 MQTT WebSocket 连接：${JSON.stringify(state)}`);
    console.log("mqtt monitor manual disconnect passed");
  } finally {
    await browser.close();
  }
} finally {
  vite.kill("SIGTERM");
  broker.close();
  await new Promise((resolve) => httpServer.close(resolve));
}
