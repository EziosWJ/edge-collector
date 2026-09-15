import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const port = 4177;
const baseUrl = `http://127.0.0.1:${port}`;
const vite = spawn(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(port)], {
  cwd: new URL("..", import.meta.url),
  stdio: ["ignore", "pipe", "pipe"],
});

async function waitForServer() {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const response = await fetch(`${baseUrl}/tests/register-format.html`);
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
    await page.goto(`${baseUrl}/tests/register-format.html`, { waitUntil: "networkidle" });
    const actual = await page.evaluate(async () => {
      const { formatRegisterValue } = await import("/src/lib/register-format.ts");
      return {
        hex: formatRegisterValue(0x2a, "hex"),
        dec: formatRegisterValue(0x2a, "dec"),
        bin: formatRegisterValue(0x2a, "bin"),
        empty: formatRegisterValue(null, "hex"),
      };
    });

    assert.deepEqual(actual, {
      hex: "0x002A",
      dec: "42",
      bin: "0b0000000000101010",
      empty: "暂无数据",
    });
    console.log("register format passed", JSON.stringify(actual));
  } finally {
    await browser.close();
  }
} finally {
  vite.kill("SIGTERM");
}
