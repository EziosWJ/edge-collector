import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";

const port = 4186;
const baseUrl = `http://127.0.0.1:${port}`;
const vite = spawn(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(port)], {
  cwd: new URL("..", import.meta.url),
  stdio: ["ignore", "pipe", "pipe"],
});

const terminalRecord = {
  commandId: "cmd-0001",
  deviceId: "device-01",
  name: "read-registers",
  status: "SUCCEEDED",
  receivedAt: "2026-09-18T16:30:00Z",
  issuedAt: "2026-09-18T16:30:00Z",
  expiresAt: "2026-09-18T16:35:00Z",
  startedAt: "2026-09-18T16:30:01Z",
  completedAt: "2026-09-18T16:30:02Z",
  errorType: null,
  errorMessage: null,
};

const runningRecord = {
  commandId: "cmd-running",
  deviceId: "device-02",
  name: "restart",
  status: "RUNNING",
  receivedAt: "2026-09-18T16:31:00Z",
  issuedAt: "2026-09-18T16:31:00Z",
  expiresAt: "2026-09-18T16:36:00Z",
  startedAt: "2026-09-18T16:31:01Z",
  completedAt: null,
  errorType: null,
  errorMessage: null,
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

function createBackend() {
  const requests = [];
  let listResponses = [];
  let defaultRecords = [terminalRecord];
  let total = 45;
  let detail = { ...terminalRecord };
  return {
    requests,
    setListResponses(responses) {
      listResponses = responses;
    },
    setDefaultRecords(records, nextTotal = 45) {
      defaultRecords = records;
      total = nextTotal;
    },
    setDetail(value) {
      detail = value;
    },
    handler: async (route) => {
      const request = route.request();
      const url = new URL(request.url());
      const path = url.pathname;
      if (path.endsWith("/commands") && request.method() === "GET") {
        const query = Object.fromEntries(url.searchParams.entries());
        requests.push({ path, query });
        const next = listResponses.length > 0 ? listResponses.shift() : null;
        const records = next?.records ?? defaultRecords;
        const pageSize = Number(query.pageSize ?? 20);
        const page = Number(query.page ?? 1);
        await route.fulfill({
          contentType: "application/json",
          body: envelope({
            records,
            total: next?.total ?? total,
            page,
            pageSize,
          }),
        });
        return;
      }
      const detailMatch = path.match(/\/commands\/([^/]+)$/);
      if (detailMatch && request.method() === "GET") {
        requests.push({ path, commandId: decodeURIComponent(detailMatch[1]) });
        await route.fulfill({ contentType: "application/json", body: envelope(detail) });
        return;
      }
      await route.continue();
    },
  };
}

async function openCommands(browser, { permissions = "mqtt:command:list,mqtt:command:detail", route = "/mqtt/commands", backend }) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error));
  await page.route("**/api/v1/mqtt/**", backend.handler);
  const query = new URLSearchParams({ permissions, route });
  await page.goto(`${baseUrl}/tests/mqtt-admin.html?${query.toString()}`, { waitUntil: "networkidle" });
  await page.getByTestId("route-commands").waitFor();
  return { page, errors };
}

function listRequests(backend) {
  return backend.requests.filter((item) => item.path.endsWith("/commands"));
}

async function waitForCount(getValue, expected, timeout = 3000) {
  const started = Date.now();
  while (Date.now() - started < timeout) {
    if (getValue() >= expected) return;
    await delay(50);
  }
  throw new Error(`等待计数 ${expected} 超时，当前 ${getValue()}`);
}

async function runCase(name, callback) {
  try {
    await callback();
    console.log(`mqtt commands: ${name} passed`);
  } catch (error) {
    console.error(`mqtt commands: ${name} failed`);
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
    await runCase("default filters, filters and pagination", async () => {
      const backend = createBackend();
      backend.setDefaultRecords([terminalRecord], 45);
      const { page, errors } = await openCommands(browser, { backend });
      try {
        await page.getByText("cmd-0001", { exact: true }).waitFor();
        assert.equal(await page.getByText("2026-09-19 00:30:00", { exact: true }).first().isVisible(), true, "时间应按 Asia/Shanghai 格式化");
        let requests = listRequests(backend);
        assert.equal(requests.length, 1);
        assert.deepEqual(requests[0].query, { page: "1", pageSize: "20" });
        const sizeOptions = await page.getByLabel("每页条数").locator("option").evaluateAll((options) => options.map((option) => option.value));
        assert.deepEqual(sizeOptions, ["10", "20", "50", "100"]);

        await page.getByLabel("按 Command ID 查询").fill("cmd-12");
        await page.getByLabel("按命令名称查询").fill("重启");
        await page.getByLabel("按 deviceId 筛选").fill("device-01");
        await page.getByLabel("按 Command 状态筛选").selectOption("FAILED");
        await page.getByRole("button", { name: "查询", exact: true }).click();
        await waitForCount(() => listRequests(backend).length, 2);
        requests = listRequests(backend);
        assert.deepEqual(requests[1].query, {
          page: "1",
          pageSize: "20",
          commandId: "cmd-12",
          name: "重启",
          deviceId: "device-01",
          status: "FAILED",
        });

        await page.getByRole("button", { name: "下一页", exact: true }).click();
        await waitForCount(() => listRequests(backend).length, 3);
        assert.deepEqual(listRequests(backend)[2].query, {
          page: "2",
          pageSize: "20",
          commandId: "cmd-12",
          name: "重启",
          deviceId: "device-01",
          status: "FAILED",
        });

        await page.getByLabel("每页条数").selectOption("50");
        await waitForCount(() => listRequests(backend).length, 4);
        const fourth = listRequests(backend)[3].query;
        assert.equal(fourth.page, "1");
        assert.equal(fourth.pageSize, "50");

        await page.getByRole("button", { name: "重置", exact: true }).click();
        await waitForCount(() => listRequests(backend).length, 5);
        assert.deepEqual(listRequests(backend)[4].query, { page: "1", pageSize: "50" });
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("status query parameter prefilters journal", async () => {
      const backend = createBackend();
      const { page, errors } = await openCommands(browser, { backend, route: "/mqtt/commands?status=FAILED" });
      try {
        await page.getByText("cmd-0001", { exact: true }).waitFor();
        assert.equal(await page.getByLabel("按 Command 状态筛选").inputValue(), "FAILED");
        assert.equal(listRequests(backend)[0].query.status, "FAILED");
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("5s refresh only while accepted or running and page is visible", async () => {
      const backend = createBackend();
      backend.setDefaultRecords([runningRecord], 1);
      backend.setListResponses([{ records: [runningRecord], total: 1 }, { records: [terminalRecord], total: 1 }]);
      const { page, errors } = await openCommands(browser, { backend });
      try {
        await page.getByText("cmd-running", { exact: true }).waitFor();
        const delays = await page.evaluate(() => window.mqttAdminTest.intervalDelays());
        assert.ok(delays.includes(5000), `应注册 5 秒刷新定时器：${JSON.stringify(delays)}`);

        await page.evaluate(() => {
          Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
        });
        await page.evaluate(() => window.mqttAdminTest.triggerIntervals(5000));
        await delay(200);
        assert.equal(listRequests(backend).length, 1, "页面不可见时不应自动刷新");

        await page.evaluate(() => {
          delete document.visibilityState;
        });
        await page.evaluate(() => window.mqttAdminTest.triggerIntervals(5000));
        await waitForCount(() => listRequests(backend).length, 2);
        await page.getByText("cmd-0001", { exact: true }).waitFor();
        const delaysAfter = await page.evaluate(() => window.mqttAdminTest.intervalDelays());
        assert.equal(delaysAfter.includes(5000), false, `全部终态后应停止自动刷新：${JSON.stringify(delaysAfter)}`);
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("terminal records do not schedule refresh", async () => {
      const backend = createBackend();
      backend.setDefaultRecords([terminalRecord], 1);
      const { page, errors } = await openCommands(browser, { backend });
      try {
        await page.getByText("cmd-0001", { exact: true }).waitFor();
        await delay(100);
        const delays = await page.evaluate(() => window.mqttAdminTest.intervalDelays());
        assert.equal(delays.includes(5000), false, `终态记录不应注册刷新定时器：${JSON.stringify(delays)}`);
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("detail dialog shows safe metadata only", async () => {
      const backend = createBackend();
      backend.setDetail({
        ...terminalRecord,
        errorType: "AUTH",
        errorMessage: "broker rejected password=topsecret ciphertext=abcdef",
        payload: "PAYLOAD-MUST-NOT-RENDER",
        resultPayload: "RESULT-MUST-NOT-RENDER",
        payloadHash: "HASH-MUST-NOT-RENDER",
        ciphertext: "CIPHERTEXT-MUST-NOT-RENDER",
        credential: "CREDENTIAL-MUST-NOT-RENDER",
      });
      const { page, errors } = await openCommands(browser, { backend });
      try {
        await page.getByText("cmd-0001", { exact: true }).waitFor();
        await page.getByRole("button", { name: "详情", exact: true }).click();
        const dialog = page.getByRole("dialog");
        await dialog.getByText("Command Journal 详情", { exact: true }).waitFor();
        await dialog.getByText("幂等事实源", { exact: true }).waitFor();
        await dialog.getByText("cmd-0001", { exact: false }).first().waitFor();
        await dialog.getByText("device-01", { exact: true }).waitFor();
        await dialog.getByText("read-registers", { exact: true }).waitFor();
        await dialog.getByText("AUTH", { exact: true }).waitFor();
        await dialog.getByText("错误信息不可展示", { exact: true }).waitFor();
        const text = await dialog.innerText();
        for (const sensitive of [
          "PAYLOAD-MUST-NOT-RENDER",
          "RESULT-MUST-NOT-RENDER",
          "HASH-MUST-NOT-RENDER",
          "CIPHERTEXT-MUST-NOT-RENDER",
          "CREDENTIAL-MUST-NOT-RENDER",
          "topsecret",
        ]) {
          assert.equal(text.includes(sensitive), false, `详情弹窗泄露敏感内容：${sensitive}`);
        }
        assert.equal(backend.requests.some((item) => item.commandId === "cmd-0001"), true);
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    await runCase("detail action requires detail permission", async () => {
      const backend = createBackend();
      const { page, errors } = await openCommands(browser, { backend, permissions: "mqtt:command:list" });
      try {
        await page.getByText("cmd-0001", { exact: true }).waitFor();
        assert.equal(await page.getByRole("button", { name: "详情", exact: true }).count(), 0, "无详情权限时不应显示详情按钮");
        await page.getByText("cmd-0001", { exact: true }).click();
        await delay(100);
        assert.equal(await page.getByRole("dialog").count(), 0, "无详情权限时点击行不应打开详情");
        assert.equal(errors.length, 0, errors.map((error) => error.message).join("\n"));
      } finally {
        await page.close();
      }
    });

    console.log("mqtt commands browser behavior passed");
  } finally {
    await browser.close();
  }
} finally {
  vite.kill("SIGTERM");
}
