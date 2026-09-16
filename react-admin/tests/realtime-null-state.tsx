import { createRoot } from "react-dom/client";
import { StateDetail } from "@/pages/acquisition/realtime";
import type { AcquisitionCurrentState } from "@/types";

const state = {
  deviceId: 1,
  deviceName: "测试设备",
  channelId: 1,
  unitId: 1,
  registerBlocks: null,
  status: "INITIAL",
  consecutiveFailures: 0,
} as unknown as AcquisitionCurrentState;

createRoot(document.getElementById("root")!).render(
  <StateDetail state={state} displayMode="hex" />,
);
