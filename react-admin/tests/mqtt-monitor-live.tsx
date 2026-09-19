import React from "react";
import ReactDOM from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { createMqttMonitorClient } from "@/lib/mqtt-monitor-client";
import { MqttMonitorPage } from "@/pages/mqtt/monitor";
import "@/styles/globals.css";

let createdClientCount = 0;

ReactDOM.createRoot(document.getElementById("root")!).render(
  <MemoryRouter initialEntries={["/mqtt/monitor"]}>
    <MqttMonitorPage clientFactory={(settings) => {
      createdClientCount += 1;
      return createMqttMonitorClient(settings);
    }} />
  </MemoryRouter>,
);

Object.assign(window, {
  mqttMonitorLiveTest: {
    createdClientCount: () => createdClientCount,
  },
});
