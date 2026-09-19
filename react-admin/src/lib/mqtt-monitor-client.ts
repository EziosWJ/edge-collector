import mqtt, {
  type IClientOptions,
  type MqttClient,
  type OnErrorCallback,
  type OnMessageCallback,
} from "mqtt";
import type {
  MqttMonitorClient,
  MqttMonitorClientEventMap,
  MqttMonitorSettings,
} from "./mqtt-monitor";

function protocolVersion(protocol: MqttMonitorSettings["protocol"]) {
  return protocol === "MQTT_5" ? 5 : 4;
}

export function createMqttMonitorClient(
  settings: MqttMonitorSettings & { clientId: string },
): MqttMonitorClient {
  const options: IClientOptions = {
    protocolVersion: protocolVersion(settings.protocol),
    clientId: settings.clientId,
    clean: true,
    reconnectPeriod: 2000,
    reconnectOnConnackError: false,
    connectTimeout: 10000,
    resubscribe: false,
    username: settings.username.trim() || undefined,
    password: settings.password || undefined,
  };
  const client = mqtt.connect(settings.wsUrl.trim(), options);

  return {
    on(event, listener) {
      if (event === "message") {
        client.on("message", listener as OnMessageCallback);
        return;
      }
      if (event === "error") {
        client.on("error", listener as OnErrorCallback);
        return;
      }
      client.on(event as keyof MqttMonitorClientEventMap, listener as never);
    },
    off(event, listener) {
      client.removeListener(event as never, listener as never);
    },
    subscribe(topic, callback) {
      client.subscribe(topic, { qos: 1 }, (error) => callback(error));
    },
    unsubscribe(topic) {
      client.unsubscribe(topic);
    },
    end(force = false) {
      client.end(force);
    },
  } satisfies MqttMonitorClient;
}

export type { MqttClient };
