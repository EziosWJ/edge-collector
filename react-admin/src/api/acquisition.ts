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
} from "@/types/acquisition";

const BASE_PATH = "/api/v1/acquisition";

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
