import React from "react";
import ReactDOM from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { MqttMonitorPage } from "@/pages/mqtt/monitor";
import type {
  MqttMonitorClient,
  MqttMonitorClientEventMap,
  MqttMonitorClientFactory,
  MqttMonitorPacket,
} from "@/lib/mqtt-monitor";
import "@/styles/globals.css";

type EventName = keyof MqttMonitorClientEventMap;
type Listener = (...args: never[]) => void;

class FakeMonitorClient implements MqttMonitorClient {
  readonly subscriptions: string[] = [];
  readonly unsubscribed: string[] = [];
  ended = false;
  private listeners = new Map<EventName, Set<Listener>>();

  on<K extends EventName>(event: K, listener: MqttMonitorClientEventMap[K]) {
    const listeners = this.listeners.get(event) ?? new Set<Listener>();
    listeners.add(listener as Listener);
    this.listeners.set(event, listeners);
  }

  off<K extends EventName>(event: K, listener: MqttMonitorClientEventMap[K]) {
    this.listeners.get(event)?.delete(listener as Listener);
  }

  subscribe(topic: string, callback: (error?: Error | null) => void) {
    this.subscriptions.push(topic);
    callback(null);
  }

  unsubscribe(topic: string) {
    this.unsubscribed.push(topic);
  }

  end() {
    this.ended = true;
  }

  emit<K extends EventName>(event: K, ...args: Parameters<MqttMonitorClientEventMap[K]>) {
    this.listeners.get(event)?.forEach((listener) => listener(...(args as never[])));
  }
}

const clients: FakeMonitorClient[] = [];
const factory: MqttMonitorClientFactory = () => {
  const client = new FakeMonitorClient();
  clients.push(client);
  return client;
};

const root = ReactDOM.createRoot(document.getElementById("root")!);
root.render(
  <MemoryRouter initialEntries={["/mqtt/monitor"]}>
    <MqttMonitorPage clientFactory={factory} />
  </MemoryRouter>,
);

Object.assign(window, {
  mqttMonitorTest: {
    clients,
    emitMessage(topic: string, payload: string, packet: MqttMonitorPacket = {}) {
      clients.at(-1)?.emit("message", topic, new TextEncoder().encode(payload), packet);
    },
    connect() {
      clients.at(-1)?.emit("connect");
    },
  },
});
