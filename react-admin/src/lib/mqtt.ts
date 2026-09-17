import { getErrorMessage } from "@/lib/api-error";
import type {
  MqttConfigUpdateRequest,
  MqttSecretAction,
} from "@/types/mqtt";

export type MqttConfigFormValues = Omit<
  MqttConfigUpdateRequest,
  "password" | "clientPrivateKey"
> & {
  password: string;
  passwordConfigured: boolean;
  clientPrivateKey: string;
  clientPrivateKeyConfigured: boolean;
};

/**
 * Build the update body without ever sending a value for a kept or cleared
 * secret. The configured flags are UI-only and are intentionally discarded.
 */
export function buildMqttConfigUpdate(
  values: MqttConfigFormValues,
): MqttConfigUpdateRequest {
  const {
    password,
    passwordConfigured,
    clientPrivateKey,
    clientPrivateKeyConfigured,
    ...plainValues
  } = values;
  void passwordConfigured;
  void clientPrivateKeyConfigured;

  const payload: MqttConfigUpdateRequest = { ...plainValues };
  if (values.passwordAction === "set") {
    payload.password = password;
  }
  if (values.clientPrivateKeyAction === "set") {
    payload.clientPrivateKey = clientPrivateKey;
  }

  return payload;
}

export function getMqttErrorMessage(
  error: unknown,
  fallback: string,
  secrets: readonly string[] = [],
) {
  const message = getErrorMessage(error, fallback);
  const containsSecret = secrets.some(
    (secret) => secret.length > 0 && message.includes(secret),
  );
  const containsSensitiveReference =
    /ciphertext|password|private[\s_-]*key|secret/i.test(message);
  return containsSecret || containsSensitiveReference ? fallback : message || fallback;
}

export function formatMqttBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes < 0) return "-";
  if (bytes < 1024) return `${Math.round(bytes)} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  if (bytes < 1024 * 1024 * 1024) {
    return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
  }
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GiB`;
}

export function getSecretActionLabel(action: MqttSecretAction) {
  switch (action) {
    case "keep":
      return "保持已配置值";
    case "set":
      return "设置新值";
    case "clear":
      return "清除配置值";
  }
}
