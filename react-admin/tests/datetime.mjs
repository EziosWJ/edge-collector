import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const port = 4176;
const baseUrl = `http://127.0.0.1:${port}`;
const vite = spawn(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(port)], {
  cwd: new URL("..", import.meta.url),
  stdio: ["ignore", "pipe", "pipe"],
});

async function waitForServer() {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const response = await fetch(`${baseUrl}/tests/datetime.html`);
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
    await page.goto(`${baseUrl}/tests/datetime.html`, { waitUntil: "networkidle" });
    const actual = await page.evaluate(async () => {
      const { formatDateOnly, formatDateTime } = await import("/src/lib/datetime.ts");
      return {
        dateTime: formatDateTime("2026-09-14T05:43:46.123456789Z"),
        midnight: formatDateTime("2026-09-13T16:30:00Z"),
        dateOnly: formatDateOnly("2026-09-13T16:30:00Z"),
        invalid: formatDateTime("not-a-date"),
      };
    });

    assert.deepEqual(actual, {
      dateTime: "2026-09-14 13:43:46",
      midnight: "2026-09-14 00:30:00",
      dateOnly: "2026-09-14",
      invalid: "-",
    });
    console.log("datetime formatting passed", JSON.stringify(actual));
  } finally {
    await browser.close();
  }
} finally {
  vite.kill("SIGTERM");
}
