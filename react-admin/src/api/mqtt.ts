import { http } from "@/lib/http";
import type {
  MqttCommandJournal,
  MqttCommandListQuery,
  MqttCommandPage,
  MqttConfig,
  MqttConfigUpdateRequest,
  MqttOutboxStats,
  MqttRuntimeState,
  MqttTestConnectionResult,
} from "@/types/mqtt";

const BASE_PATH = "/api/v1/mqtt";

export function getMqttConfig() {
  return http.get<MqttConfig>(`${BASE_PATH}/config`);
}

export function updateMqttConfig(data: MqttConfigUpdateRequest) {
  return http.put<MqttConfig>(`${BASE_PATH}/config`, data);
}

export function testMqttConnection(data?: MqttConfigUpdateRequest) {
  return http.post<MqttTestConnectionResult>(
    `${BASE_PATH}/test-connection`,
    data,
  );
}

export function getMqttState() {
  return http.get<MqttRuntimeState>(`${BASE_PATH}/state`);
}

export function getMqttOutboxStats() {
  return http.get<MqttOutboxStats>(`${BASE_PATH}/outbox/stats`);
}

export function getMqttCommands(query: MqttCommandListQuery = {}) {
  return http.get<MqttCommandPage>(`${BASE_PATH}/commands`, { query });
}

export function getMqttCommand(commandId: string) {
  return http.get<MqttCommandJournal>(
    `${BASE_PATH}/commands/${encodeURIComponent(commandId)}`,
  );
}
