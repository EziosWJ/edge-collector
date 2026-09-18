import { Navigate } from "react-router-dom";
import { hasPermission } from "@/lib/permission";

export function MqttManagementPage() {
  const target = hasPermission("mqtt:overview:list")
    ? "/mqtt/overview"
    : hasPermission("mqtt:config:list")
      ? "/mqtt/config"
      : hasPermission("mqtt:command:list")
        ? "/mqtt/commands"
        : "/mqtt/overview";
  return <Navigate to={target} replace />;
}
