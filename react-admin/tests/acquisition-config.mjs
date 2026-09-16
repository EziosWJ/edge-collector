import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const port = 4180;
const baseUrl = `http://127.0.0.1:${port}`;
const vite = spawn(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(port)], {
  cwd: new URL("..", import.meta.url),
  stdio: ["ignore", "pipe", "pipe"],
});

async function waitForServer() {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const response = await fetch(`${baseUrl}/tests/acquisition-config.html`);
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
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
    const pageErrors = [];
    page.on("pageerror", (error) => pageErrors.push(error));
    await page.goto(`${baseUrl}/tests/acquisition-config.html`, { waitUntil: "networkidle" });
    await page.getByText("192.0.2.10:1502", { exact: true }).waitFor();
    assert.equal(await page.getByText("192.0.2.10:1502", { exact: true }).count(), 1);

    await page.getByRole("button", { name: "新建设备", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("heading", { name: "新建设备", exact: true }).waitFor();
    const channel = dialog.locator('select[name="channelId"]');
    const unitID = dialog.locator('input[name="unitId"]');
    await channel.selectOption("2");
    assert.equal(await unitID.getAttribute("min"), "1");
    assert.equal(await unitID.getAttribute("max"), "247");

    await dialog.locator('input[name="name"]').fill("非法 Unit ID 设备");
    await dialog.locator('input[name="registerBlocks.0.name"]').fill("holding");
    await unitID.fill("248");
    await dialog.getByRole("button", { name: "保存", exact: true }).click();
    await dialog.getByText("Modbus RTU over UDP Unit ID 范围为 1～247", { exact: true }).waitFor();
    assert.equal(pageErrors.length, 0, pageErrors.map((error) => error.message).join("\n"));
    console.log("acquisition config passed");
  } finally {
    await browser.close();
  }
} finally {
  vite.kill("SIGTERM");
}
