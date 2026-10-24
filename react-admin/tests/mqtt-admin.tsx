/* eslint-disable react-refresh/only-export-components */
import React from "react";
import ReactDOM from "react-dom/client";
import {
  createMemoryRouter,
  Link,
  Outlet,
  RouterProvider,
} from "react-router-dom";
import { ToastProvider } from "@/components/common/toast";
import { defaultNavItems, mergeNavItems } from "@/config/navigation";
import type {
  MqttMonitorClient,
  MqttMonitorClientEventMap,
  MqttMonitorClientFactory,
  MqttMonitorPacket,
} from "@/lib/mqtt-monitor";
import { mockCurrentUserPermissionCodes } from "@/mocks/rbac";
import { MqttManagementPage } from "@/pages/mqtt";
import { MqttCommandsPage } from "@/pages/mqtt/commands";
import { MqttConfigPage } from "@/pages/mqtt/config";
import { MqttMonitorPage } from "@/pages/mqtt/monitor";
import { MqttOverviewPage } from "@/pages/mqtt/overview";
import "@/styles/globals.css";

type EventName = keyof MqttMonitorClientEventMap;
type Listener = (...args: never[]) => void;

class FakeMonitorClient implements MqttMonitorClient {
  readonly subscriptions: string[] = [];
  readonly unsubscribed: string[] = [];
  ended = false;
  endForce: boolean | undefined;
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

  end(force?: boolean) {
    this.ended = true;
    this.endForce = force;
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

const params = new URLSearchParams(window.location.search);
const permissionParam = params.get("permissions");
if (permissionParam !== null) {
  const codes = permissionParam === "none" || permissionParam === ""
    ? []
    : permissionParam.split(",").map((item) => item.trim()).filter(Boolean);
  mockCurrentUserPermissionCodes.splice(0, mockCurrentUserPermissionCodes.length, ...codes);
}

const initialPath = params.get("route") ?? "/mqtt";

type FakeInterval = { id: number; handler: TimerHandler; ms: number };
const intervals = new Map<number, FakeInterval>();
let intervalSequence = 1;
const originalSetInterval = window.setInterval.bind(window);
const originalClearInterval = window.clearInterval.bind(window);
window.setInterval = ((handler: TimerHandler, ms?: number) => {
  const id = intervalSequence++;
  intervals.set(id, { id, handler, ms: ms ?? 0 });
  return id;
}) as typeof window.setInterval;
window.clearInterval = ((id?: number) => {
  if (typeof id === "number") intervals.delete(id);
}) as typeof window.clearInterval;

function triggerIntervals(ms: number) {
  return [...intervals.values()].filter((item) => item.ms === ms).map((item) => {
    if (typeof item.handler === "function") {
      (item.handler as () => void)();
      return true;
    }
    return false;
  });
}

function Shell() {
  const navItems = mergeNavItems(defaultNavItems, []);
  return (
    <div>
      <nav data-testid="permission-nav">
        {navItems.map((item) => (
          <span key={item.path} data-nav-path={item.path}>
            {item.label}
            {item.children?.map((child) => (
              <span key={child.path} data-nav-path={child.path}>{child.label}</span>
            ))}
          </span>
        ))}
      </nav>
      <Link data-testid="go-elsewhere" to="/elsewhere">离开当前页面</Link>
      <Outlet />
    </div>
  );
}

function PageMarker({ name }: { name: string }) {
  return <div data-testid={`route-${name}`}>route:{name}</div>;
}

const router = createMemoryRouter(
  [
    {
      element: <Shell />,
      children: [
        { path: "/mqtt", element: <><PageMarker name="entry" /><MqttManagementPage /></> },
        { path: "/mqtt/overview", element: <><PageMarker name="overview" /><MqttOverviewPage /></> },
        { path: "/mqtt/config", element: <><PageMarker name="config" /><MqttConfigPage /></> },
        { path: "/mqtt/commands", element: <><PageMarker name="commands" /><MqttCommandsPage /></> },
        { path: "/mqtt/monitor", element: <><PageMarker name="monitor" /><MqttMonitorPage clientFactory={factory} /></> },
        { path: "/elsewhere", element: <PageMarker name="elsewhere" /> },
      ],
    },
  ],
  { initialEntries: [initialPath] },
);

const root = ReactDOM.createRoot(document.getElementById("root")!);
root.render(<ToastProvider><RouterProvider router={router} /></ToastProvider>);

Object.assign(window, {
  mqttAdminTest: {
    clients,
    factory,
    emitMessage(topic: string, payload: string, packet: MqttMonitorPacket = {}) {
      clients.at(-1)?.emit("message", topic, new TextEncoder().encode(payload), packet);
    },
    emitConnect() {
      clients.at(-1)?.emit("connect");
    },
    emitReconnect() {
      clients.at(-1)?.emit("reconnect");
    },
    emitOffline() {
      clients.at(-1)?.emit("offline");
    },
    emitClose() {
      clients.at(-1)?.emit("close");
    },
    emitError(message: string) {
      clients.at(-1)?.emit("error", new Error(message));
    },
    intervalDelays() {
      return [...intervals.values()].map((item) => item.ms);
    },
    activeIntervalCount() {
      return intervals.size;
    },
    triggerIntervals,
    permissionCodes() {
      return [...mockCurrentUserPermissionCodes];
    },
    originalSetInterval,
    originalClearInterval,
  },
});
