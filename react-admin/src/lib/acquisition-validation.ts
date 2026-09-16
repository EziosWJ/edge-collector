import type { AcquisitionProtocol } from "@/types";

export type UnitIDRange = { min: 0 | 1; max: 247 | 255; message: string };

export function getUnitIDRange(protocol?: AcquisitionProtocol): UnitIDRange {
  if (protocol === "MODBUS_RTU" || protocol === "MODBUS_RTU_OVER_UDP") {
    const label = protocol === "MODBUS_RTU" ? "Modbus RTU" : "Modbus RTU over UDP";
    return { min: 1, max: 247, message: `${label} Unit ID 范围为 1～247` };
  }
  return { min: 0, max: 255, message: "网络协议 Unit ID 范围为 0～255" };
}
