import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const port = 4179;
const baseUrl = `http://127.0.0.1:${port}`;
const vite = spawn(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(port)], {
  cwd: new URL("..", import.meta.url),
  stdio: ["ignore", "pipe", "pipe"],
});

async function waitForServer() {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const response = await fetch(`${baseUrl}/tests/realtime-layout.html`);
      if (response.ok) return;
    } catch {
      // Vite is still starting.
    }
    await delay(250);
  }
  throw new Error("Vite test server did not start");
}

async function openPage(browser, options = {}) {
  const { scenario = "", ...contextOptions } = options;
  const page = await browser.newPage(contextOptions);
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error));
  await page.goto(`${baseUrl}/tests/realtime-layout.html${scenario}`, { waitUntil: "networkidle" });
  await page.getByTestId("realtime-page").waitFor();
  await page.getByTestId("summary-total").waitFor();
  if (!scenario) {
    await page.getByRole("heading", { name: "原始寄存器", exact: true }).waitFor();
  }
  await page.waitForTimeout(100);
  assert.equal(pageErrors.length, 0, pageErrors.map((error) => error.message).join("\n"));
  return page;
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
    const desktop = await openPage(browser, { viewport: { width: 1280, height: 900 } });
    await desktop.getByTestId("summary-total").waitFor();
    assert.equal(await desktop.getByTestId("summary-total").innerText(), "5");
    assert.equal(await desktop.getByTestId("summary-online").innerText(), "1");
    assert.equal(await desktop.getByTestId("summary-degraded").innerText(), "1");
    assert.equal(await desktop.getByTestId("summary-unconfigured").innerText(), "1");
    assert.equal(await desktop.getByTestId("summary-offline").innerText(), "1");
    for (const [channelID, status] of [[10, "空闲"], [11, "启动中"], [12, "在线"], [13, "部分失败"], [14, "离线"]]) {
      const card = desktop.getByTestId(`channel-runtime-${channelID}`);
      await card.getByText(status, { exact: true }).waitFor();
    }
    await desktop.getByTestId("register-display-toolbar").waitFor();
    await desktop.getByRole("heading", { name: "原始寄存器", exact: true }).waitFor();
    await desktop.getByText("FC03 · 地址 0–2 · 3 个寄存器 · 3/3 有效", { exact: true }).waitFor();
    await desktop.getByRole("columnheader", { name: "地址（十进制）", exact: true }).waitFor();
    await desktop.getByRole("columnheader", { name: "值（HEX）", exact: true }).waitFor();
    assert.equal(await desktop.getByText("0x0BB8", { exact: true }).count(), 1);
    assert.equal(await desktop.getByText("有效", { exact: true }).count(), 3);

    const desktopLayout = await desktop.evaluate(() => {
      const nav = document.querySelector('[data-testid="realtime-device-nav"]');
      return {
        viewportWidth: document.documentElement.clientWidth,
        pageWidth: document.documentElement.scrollWidth,
        navWidth: nav?.getBoundingClientRect().width ?? 0,
      };
    });
    assert.ok(desktopLayout.pageWidth <= desktopLayout.viewportWidth + 1, JSON.stringify(desktopLayout));
    assert.ok(desktopLayout.navWidth >= 280 && desktopLayout.navWidth <= 340, JSON.stringify(desktopLayout));

    await desktop.getByRole("button", { name: "DEC", exact: true }).click();
    assert.equal(await desktop.getByTestId("register-value-0").innerText(), "3000");
    await desktop.getByTestId("device-nav-2").click();
    await desktop.getByTestId("state-detail-title").getByText("南侧馈电保护器").waitFor();
    await desktop.getByText("FC04 · 地址 16–17 · 2 个寄存器 · 0/2 有效", { exact: true }).waitFor();
    assert.equal(await desktop.getByText("42", { exact: true }).count(), 1);
    assert.equal(await desktop.getByText("暂无数据", { exact: true }).count(), 1);
    assert.equal(await desktop.getByText("无效", { exact: true }).count(), 2);
    await desktop.getByText("部分失败").last().waitFor();
    await desktop.getByTestId("device-nav-3").click();
    await desktop.getByText("未配置读取块").last().waitFor();
    await desktop.getByTestId("device-nav-5").click();
    await desktop.getByText("等待首次采集").last().waitFor();
    await desktop.getByTestId("device-nav-4").click();
    await desktop.getByText("离线").last().waitFor();
    await desktop.getByTestId("device-nav-2").focus();
    assert.equal(await desktop.evaluate(() => document.activeElement?.tagName), "BUTTON");
    await desktop.close();

    const narrow = await openPage(browser, { viewport: { width: 375, height: 800 } });
    const narrowLayout = await narrow.evaluate(() => {
      const localTable = document.querySelector(".realtime-register-table");
      return {
        viewportWidth: document.documentElement.clientWidth,
        pageWidth: document.documentElement.scrollWidth,
        localTableWidth: localTable?.clientWidth ?? 0,
        localTableScrollWidth: localTable?.scrollWidth ?? 0,
      };
    });
    assert.ok(narrowLayout.pageWidth <= narrowLayout.viewportWidth + 1, JSON.stringify(narrowLayout));
    assert.ok(narrowLayout.localTableScrollWidth > narrowLayout.localTableWidth, JSON.stringify(narrowLayout));
    await narrow.close();

    const empty = await openPage(browser, { scenario: "?scenario=empty", viewport: { width: 1280, height: 900 } });
    await empty.getByText("暂无运行中设备").waitFor();
    await empty.getByText("选择一个设备").waitFor();
    await empty.close();

    const error = await openPage(browser, { scenario: "?scenario=error", viewport: { width: 1280, height: 900 } });
    await error.getByText("加载失败").waitFor();
    await error.getByText("实时状态接口不可用").waitFor();
    await error.close();

    const flaky = await openPage(browser, { scenario: "?scenario=flaky", viewport: { width: 1280, height: 900 } });
    await flaky.getByRole("heading", { name: "原始寄存器", exact: true }).waitFor();
    await flaky.waitForTimeout(1700);
    await flaky.getByText("本次刷新失败，当前显示上次状态", { exact: true }).waitFor();
    assert.equal(await flaky.getByTestId("summary-total").innerText(), "5");
    await flaky.getByTestId("device-nav-1").waitFor();
    await flaky.close();
    console.log("realtime layout passed");
  } finally {
    await browser.close();
  }
} finally {
  vite.kill("SIGTERM");
}
