import { createRoot } from "react-dom/client";
import { AcquisitionRealtimePage } from "@/pages/acquisition/realtime";
import type { AcquisitionCurrentState } from "@/types";
import "@/styles/globals.css";

const now = "2026-09-15T11:58:12Z";

const states: AcquisitionCurrentState[] = [
  {
    deviceId: 1,
    deviceName: "北侧馈电保护器",
    channelId: 1,
    unitId: 1,
    registerBlocks: [
      {
        id: 101,
        name: "原始寄存器",
        functionCode: 3,
        startAddress: 0,
        quantity: 3,
        sortOrder: 0,
        values: [3000, 125, 0],
        valid: true,
        lastAttemptAt: now,
        lastSuccessAt: now,
      },
    ],
    lastAttemptAt: now,
    lastSuccessAt: now,
    status: "ONLINE",
    consecutiveFailures: 0,
  },
  {
    deviceId: 2,
    deviceName: "南侧馈电保护器",
    channelId: 1,
    unitId: 2,
    registerBlocks: [
      {
        id: 102,
        name: "输入寄存器",
        functionCode: 4,
        startAddress: 16,
        quantity: 2,
        sortOrder: 0,
        values: [42, null],
        valid: false,
        lastAttemptAt: now,
        lastSuccessAt: null,
        lastError: "读取超时",
      },
    ],
    lastAttemptAt: now,
    lastSuccessAt: null,
    status: "DEGRADED",
    consecutiveFailures: 0,
    lastError: "读取超时",
  },
  {
    deviceId: 3,
    deviceName: "待配置设备",
    channelId: 2,
    unitId: 3,
    registerBlocks: [],
    lastAttemptAt: null,
    lastSuccessAt: null,
    status: "INITIAL",
    consecutiveFailures: 0,
  },
  {
    deviceId: 4,
    deviceName: "失联设备",
    channelId: 2,
    unitId: 4,
    registerBlocks: [
      {
        id: 104,
        name: "离线读取块",
        functionCode: 3,
        startAddress: 32,
        quantity: 1,
        sortOrder: 0,
        values: [null],
        valid: false,
        lastAttemptAt: now,
        lastSuccessAt: null,
        lastError: "设备无响应",
      },
    ],
    lastAttemptAt: now,
    lastSuccessAt: null,
    status: "OFFLINE",
    consecutiveFailures: 3,
    lastError: "设备无响应",
  },
  {
    deviceId: 5,
    deviceName: "等待首轮设备",
    channelId: 3,
    unitId: 5,
    registerBlocks: [
      {
        id: 105,
        name: "等待读取块",
        functionCode: 4,
        startAddress: 48,
        quantity: 1,
        sortOrder: 0,
        values: [null],
        valid: false,
        lastAttemptAt: null,
        lastSuccessAt: null,
      },
    ],
    lastAttemptAt: null,
    lastSuccessAt: null,
    status: "INITIAL",
    consecutiveFailures: 0,
  },
];

const nativeFetch = window.fetch.bind(window);
let flakyCalls = 0;
window.fetch = async (input, init) => {
  const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
  if (url.includes("/api/v1/acquisition/channel-state")) {
    return new Response(JSON.stringify({ code: 200, message: "OK", data: [] }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }
  if (!url.includes("/api/v1/acquisition/states")) {
    return nativeFetch(input, init);
  }

  const scenario = new URL(window.location.href).searchParams.get("scenario");
  if (scenario === "error" || (scenario === "flaky" && ++flakyCalls > 1)) {
    return new Response(JSON.stringify({ code: 503, message: "实时状态接口不可用" }), {
      status: 503,
      headers: { "Content-Type": "application/json" },
    });
  }

  return new Response(JSON.stringify({ code: 200, message: "OK", data: scenario === "empty" ? [] : states }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
};

createRoot(document.getElementById("root")!).render(<AcquisitionRealtimePage />);
