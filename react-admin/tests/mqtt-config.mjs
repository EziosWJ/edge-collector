import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const port = 4177;
const baseUrl = `http://127.0.0.1:${port}`;
const vite = spawn(
  process.execPath,
  ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(port)],
  {
    cwd: new URL("..", import.meta.url),
    stdio: ["ignore", "pipe", "pipe"],
  },
);

async function waitForServer() {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const response = await fetch(`${baseUrl}/tests/mqtt-config.html`);
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
    ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH }
      : {}),
  });
  try {
    const page = await browser.newPage();
    await page.goto(`${baseUrl}/tests/mqtt-config.html`, {
      waitUntil: "networkidle",
    });
    const actual = await page.evaluate(async () => {
      const { buildMqttConfigUpdate } = await import("/src/lib/mqtt.ts");
      const values = {
        enabled: true,
        edgeId: "edge-01",
        brokerUrl: "mqtt://127.0.0.1:1883",
        protocolVersion: "MQTT_5",
        clientId: "edge-collector",
        username: "operator",
        tlsEnabled: true,
        caCertificate: "CA",
        clientCertificate: "CERT",
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
        passwordConfigured: true,
        passwordAction: "keep",
        password: "broker-secret-that-must-not-leak",
        clientPrivateKeyConfigured: true,
        clientPrivateKeyAction: "keep",
        clientPrivateKey: "private-key-that-must-not-leak",
      };
      const keep = buildMqttConfigUpdate(values);
      const clear = buildMqttConfigUpdate({
        ...values,
        passwordAction: "clear",
        clientPrivateKeyAction: "clear",
      });
      const set = buildMqttConfigUpdate({
        ...values,
        passwordAction: "set",
        clientPrivateKeyAction: "set",
      });
      return {
        keep,
        clear,
        set,
        keepHasPassword: Object.hasOwn(keep, "password"),
        keepHasPrivateKey: Object.hasOwn(keep, "clientPrivateKey"),
        clearHasPassword: Object.hasOwn(clear, "password"),
        clearHasPrivateKey: Object.hasOwn(clear, "clientPrivateKey"),
      };
    });

    assert.equal(actual.keepHasPassword, false);
    assert.equal(actual.keepHasPrivateKey, false);
    assert.equal(actual.clearHasPassword, false);
    assert.equal(actual.clearHasPrivateKey, false);
    assert.equal(actual.set.password, "broker-secret-that-must-not-leak");
    assert.equal(actual.set.clientPrivateKey, "private-key-that-must-not-leak");
    console.log("MQTT secret action contract passed");
  } finally {
    await browser.close();
  }
} finally {
  vite.kill("SIGTERM");
}
