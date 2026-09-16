import { http } from "@/lib/http";
import type {
  AcquisitionChannel,
  AcquisitionChannelInput,
  AcquisitionChannelPage,
  AcquisitionChannelRuntimeState,
  AcquisitionChannelQuery,
  AcquisitionCurrentState,
  AcquisitionDevice,
  AcquisitionDeviceInput,
  AcquisitionDevicePage,
  AcquisitionDeviceQuery,
  AcquisitionScript,
  AcquisitionScriptInput,
  AcquisitionScriptPage,
  AcquisitionScriptQuery,
  AcquisitionScriptRuntimeState,
  AcquisitionScriptValidationResult,
  AcquisitionScriptVersion,
} from "@/types/acquisition";

const BASE_PATH = "/api/v1/acquisition";
const SCRIPTS_PATH = `${BASE_PATH}/scripts`;
const SCRIPT_STATES_PATH = `${BASE_PATH}/script-states`;

export function getAcquisitionChannels(query: AcquisitionChannelQuery = {}) {
  return http.get<AcquisitionChannelPage>(`${BASE_PATH}/channels`, { query });
}

export function getAcquisitionChannel(id: number) {
  return http.get<AcquisitionChannel>(`${BASE_PATH}/channels/${id}`);
}

export function createAcquisitionChannel(data: AcquisitionChannelInput) {
  return http.post<AcquisitionChannel>(`${BASE_PATH}/channels`, data);
}

export function updateAcquisitionChannel(
  id: number,
  data: AcquisitionChannelInput,
) {
  return http.put<AcquisitionChannel>(`${BASE_PATH}/channels/${id}`, data);
}

export function deleteAcquisitionChannel(id: number) {
  return http.delete<void>(`${BASE_PATH}/channels/${id}`);
}

export function getAcquisitionDevices(query: AcquisitionDeviceQuery = {}) {
  return http.get<AcquisitionDevicePage>(`${BASE_PATH}/devices`, { query });
}

export function getAcquisitionDevice(id: number) {
  return http.get<AcquisitionDevice>(`${BASE_PATH}/devices/${id}`);
}

export function createAcquisitionDevice(data: AcquisitionDeviceInput) {
  return http.post<AcquisitionDevice>(`${BASE_PATH}/devices`, data);
}

export function updateAcquisitionDevice(
  id: number,
  data: AcquisitionDeviceInput,
) {
  return http.put<AcquisitionDevice>(`${BASE_PATH}/devices/${id}`, data);
}

export function deleteAcquisitionDevice(id: number) {
  return http.delete<void>(`${BASE_PATH}/devices/${id}`);
}

export function getAcquisitionStates() {
  return http.get<AcquisitionCurrentState[]>(`${BASE_PATH}/states`);
}

export function getAcquisitionState(id: number) {
  return http.get<AcquisitionCurrentState>(`${BASE_PATH}/states/${id}`);
}

export function getAcquisitionChannelStates() {
  return http.get<AcquisitionChannelRuntimeState[]>(`${BASE_PATH}/channel-state`);
}

export function getAcquisitionScripts(query: AcquisitionScriptQuery = {}) {
  return http.get<AcquisitionScriptPage>(SCRIPTS_PATH, { query });
}

export function getAcquisitionScript(id: number) {
  return http.get<AcquisitionScript>(`${SCRIPTS_PATH}/${id}`);
}

export function createAcquisitionScript(data: AcquisitionScriptInput) {
  return http.post<AcquisitionScript>(SCRIPTS_PATH, data);
}

export function updateAcquisitionScript(id: number, data: AcquisitionScriptInput) {
  return http.put<AcquisitionScript>(`${SCRIPTS_PATH}/${id}`, data);
}

export function deleteAcquisitionScript(id: number) {
  return http.delete<void>(`${SCRIPTS_PATH}/${id}`);
}

export function validateAcquisitionScript(id: number) {
  return http.post<AcquisitionScriptValidationResult>(`${SCRIPTS_PATH}/${id}/validate`);
}

export function publishAcquisitionScript(id: number) {
  return http.post<AcquisitionScriptVersion>(`${SCRIPTS_PATH}/${id}/publish`);
}

export function getAcquisitionScriptVersions(id: number) {
  return http.get<AcquisitionScriptVersion[]>(`${SCRIPTS_PATH}/${id}/versions`);
}

export function rollbackAcquisitionScript(id: number, versionId: number) {
  return http.post<void>(`${SCRIPTS_PATH}/${id}/rollback`, { versionId });
}

export function getAcquisitionScriptStates() {
  return http.get<AcquisitionScriptRuntimeState[]>(SCRIPT_STATES_PATH);
}

export function getAcquisitionScriptState(deviceId: number) {
  return http.get<AcquisitionScriptRuntimeState>(`${SCRIPT_STATES_PATH}/${deviceId}`);
}
