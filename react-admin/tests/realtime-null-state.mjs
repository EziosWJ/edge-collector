import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const port = 4178;
const baseUrl = `http://127.0.0.1:${port}`;
const vite = spawn(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(port)], {
  cwd: new URL("..", import.meta.url),
  stdio: ["ignore", "pipe", "pipe"],
});

async function waitForServer() {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const response = await fetch(`${baseUrl}/tests/realtime-null-state.html`);
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
    const pageErrors = [];
    pageErrors.push = pageErrors.push.bind(pageErrors);
    page.on("pageerror", (error) => pageErrors.push(error));
    await page.goto(`${baseUrl}/tests/realtime-null-state.html`, { waitUntil: "networkidle" });
    await page.waitForTimeout(100);
    assert.equal(pageErrors.length, 0, pageErrors.map((error) => error.message).join("\n"));
    await assert.doesNotReject(() => page.getByText("暂无读取块").waitFor());
    await assert.doesNotReject(() => page.locator("header").getByText("未配置读取块").waitFor());
    await assert.doesNotReject(() => page.getByText("未配置读取块", { exact: true }).last().waitFor());
    console.log("realtime null state passed");
  } finally {
    await browser.close();
  }
} finally {
  vite.kill("SIGTERM");
}
