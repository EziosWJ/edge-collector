const DISPLAY_TIME_ZONE = "Asia/Shanghai";
const DATE_ONLY_PATTERN = /^\d{4}-\d{2}-\d{2}$/;

const displayFormatter = new Intl.DateTimeFormat("zh-CN", {
  timeZone: DISPLAY_TIME_ZONE,
  calendar: "gregory",
  numberingSystem: "latn",
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hourCycle: "h23",
});

function parseDate(value: string) {
  const trimmed = value.trim();
  if (!trimmed || DATE_ONLY_PATTERN.test(trimmed)) return null;

  const date = new Date(trimmed);
  return Number.isNaN(date.getTime()) ? null : date;
}

function getPart(parts: Intl.DateTimeFormatPart[], type: Intl.DateTimeFormatPartTypes) {
  return parts.find((part) => part.type === type)?.value ?? "";
}

function formatDateParts(date: Date) {
  const parts = displayFormatter.formatToParts(date);
  const year = getPart(parts, "year");
  const month = getPart(parts, "month");
  const day = getPart(parts, "day");
  const hour = getPart(parts, "hour");
  const minute = getPart(parts, "minute");
  const second = getPart(parts, "second");

  if (![year, month, day, hour, minute, second].every(Boolean)) return null;
  return { year, month, day, hour, minute, second };
}

export function formatDateOnly(value?: string | null) {
  if (!value) return "-";

  const trimmed = value.trim();
  if (!trimmed) return "-";
  if (DATE_ONLY_PATTERN.test(trimmed)) return trimmed;

  const date = parseDate(trimmed);
  if (!date) return "-";

  const parts = formatDateParts(date);
  return parts ? `${parts.year}-${parts.month}-${parts.day}` : "-";
}

export function formatDateTime(value?: string | null) {
  if (!value) return "-";

  const date = parseDate(value);
  if (!date) return "-";

  const parts = formatDateParts(date);
  return parts
    ? `${parts.year}-${parts.month}-${parts.day} ${parts.hour}:${parts.minute}:${parts.second}`
    : "-";
}
