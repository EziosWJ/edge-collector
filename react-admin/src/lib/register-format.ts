export type RegisterDisplayMode = "hex" | "dec" | "bin";

export function formatRegisterValue(value: number | null, mode: RegisterDisplayMode): string {
  if (value === null || value === undefined) return "暂无数据";
  if (mode === "hex") return `0x${value.toString(16).toUpperCase().padStart(4, "0")}`;
  if (mode === "bin") return `0b${value.toString(2).padStart(16, "0")}`;
  return String(value);
}
