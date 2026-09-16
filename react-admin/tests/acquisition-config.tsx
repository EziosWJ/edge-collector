import { createRoot } from "react-dom/client";
import { AcquisitionDevicesPage } from "@/pages/acquisition/devices";
import type { ApiResponse } from "@/types/api";
import type { AcquisitionChannel, AcquisitionDevice } from "@/types";
import "@/styles/globals.css";

const channels: AcquisitionChannel[] = [
  {
    id: 1,
    name: "TCP 测试通道",
    protocol: "MODBUS_TCP",
    timeoutMs: 500,
    interRequestDelayMs: 0,
    enabled: 1,
  },
  {
    id: 2,
    name: "RTU over UDP 测试通道",
    protocol: "MODBUS_RTU_OVER_UDP",
    timeoutMs: 500,
    interRequestDelayMs: 0,
    enabled: 1,
  },
];

const devices: AcquisitionDevice[] = [
  {
    id: 1,
    name: "TCP endpoint A",
    deviceType: "FEED_PROTECTOR",
    channelId: 1,
    unitId: 1,
    networkEndpoint: { host: "192.0.2.10", port: 1502 },
    pollIntervalMs: 1000,
    failureThreshold: 3,
    enabled: 1,
    registerBlocks: [],
  },
];

function response<T>(data: T): Response {
  const payload: ApiResponse<T> = { code: 200, message: "OK", data };
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

window.fetch = async (input, init) => {
  const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
  if (url.includes("/api/v1/acquisition/channels")) {
    return response({ records: channels, total: channels.length, page: 1, pageSize: 500 });
  }
  if (url.includes("/api/v1/acquisition/devices") && (init?.method ?? "GET") === "GET") {
    return response({ records: devices, total: devices.length, page: 1, pageSize: 10 });
  }
  if (url.includes("/api/v1/acquisition/devices") && init?.method === "POST") {
    return response(devices[0]);
  }
  throw new Error(`Unexpected fixture request: ${url}`);
};

createRoot(document.getElementById("root")!).render(<AcquisitionDevicesPage />);
