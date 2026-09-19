import { createBrowserRouter, Navigate } from "react-router-dom";
import { RequireAuth } from "@/components/auth/require-auth";
import { AppShell } from "@/components/layout/app-shell";
import { AccountProfilePage } from "@/pages/account-profile";
import { AcquisitionChannelsPage } from "@/pages/acquisition/channels";
import { AcquisitionDevicesPage } from "@/pages/acquisition/devices";
import { AcquisitionRealtimePage } from "@/pages/acquisition/realtime";
import { AcquisitionScriptsPage } from "@/pages/acquisition/scripts";
import { MqttManagementPage } from "@/pages/mqtt";
import { MqttOverviewPage } from "@/pages/mqtt/overview";
import { MqttConfigPage } from "@/pages/mqtt/config";
import { MqttCommandsPage } from "@/pages/mqtt/commands";
import { MqttMonitorPage } from "@/pages/mqtt/monitor";
import { ChangePasswordPage } from "@/pages/change-password";
import { DashboardPage } from "@/pages/dashboard";
import { DetailDemoPage } from "@/pages/examples/detail-demo";
import { FileUploadDemoPage } from "@/pages/examples/file-upload-demo";
import { FormExamplePage } from "@/pages/form-example";
import { ListDemoPage } from "@/pages/examples/list-demo";
import { TreeDemoPage } from "@/pages/examples/tree-demo";
import { TreeTableDemoPage } from "@/pages/examples/tree-table-demo";
import { LoginPage } from "@/pages/login";
import { NotFoundPage } from "@/pages/not-found";
import { SettingsPage } from "@/pages/settings";
import { SystemConfigsPage } from "@/pages/system/configs";
import { SystemDeptsPage } from "@/pages/system/depts";
import { SystemDictsPage } from "@/pages/system/dicts";
import { SystemFilesPage } from "@/pages/system/files";
import { SystemLoginLogsPage } from "@/pages/system/logs/login-logs";
import { SystemOperLogsPage } from "@/pages/system/logs/oper-logs";
import { SystemMenusPage } from "@/pages/system/menus";
import { SystemRolesPage } from "@/pages/system/roles";
import { UsersPage } from "@/pages/system/users";
import { NotificationsPage } from "@/pages/notifications";
import { NotificationManagePage } from "@/pages/system/notifications";

export const router = createBrowserRouter([
  {
    path: "/login",
    element: <LoginPage />,
  },
  {
    path: "/",
    element: (
      <RequireAuth>
        <AppShell />
      </RequireAuth>
    ),
    children: [
      {
        index: true,
        element: <Navigate to="/dashboard" replace />,
      },
      {
        path: "dashboard",
        element: <DashboardPage />,
      },
      {
        path: "system/user",
        element: <UsersPage />,
      },
      {
        path: "forms/basic",
        element: <FormExamplePage />,
      },
      {
        path: "examples/list",
        element: <ListDemoPage />,
      },
      {
        path: "examples/tree",
        element: <TreeDemoPage />,
      },
      {
        path: "examples/tree-table",
        element: <TreeTableDemoPage />,
      },
      {
        path: "examples/detail",
        element: <DetailDemoPage />,
      },
      {
        path: "examples/file-upload",
        element: <FileUploadDemoPage />,
      },
      {
        path: "settings",
        element: <SettingsPage />,
      },
      {
        path: "system",
        element: <Navigate to="/system/user" replace />,
      },
      {
        path: "acquisition/channel",
        element: <AcquisitionChannelsPage />,
      },
      {
        path: "acquisition/device",
        element: <AcquisitionDevicesPage />,
      },
      {
        path: "acquisition/script",
        element: <AcquisitionScriptsPage />,
      },
      {
        path: "acquisition/realtime",
        element: <AcquisitionRealtimePage />,
      },
      {
        path: "mqtt",
        element: <MqttManagementPage />,
      },
      {
        path: "mqtt/overview",
        element: <MqttOverviewPage />,
      },
      {
        path: "mqtt/config",
        element: <MqttConfigPage />,
      },
      {
        path: "mqtt/commands",
        element: <MqttCommandsPage />,
      },
      {
        path: "mqtt/monitor",
        element: <MqttMonitorPage />,
      },
      {
        path: "system/dept",
        element: <SystemDeptsPage />,
      },
      {
        path: "system/dict",
        element: <SystemDictsPage />,
      },
      {
        path: "system/config",
        element: <SystemConfigsPage />,
      },
      {
        path: "system/role",
        element: <SystemRolesPage />,
      },
      {
        path: "system/menu",
        element: <SystemMenusPage />,
      },
      {
        path: "system/login-log",
        element: <SystemLoginLogsPage />,
      },
      {
        path: "system/oper-log",
        element: <SystemOperLogsPage />,
      },
      {
        path: "system/file",
        element: <SystemFilesPage />,
      },
      {
        path: "notifications",
        element: <NotificationsPage />,
      },
      {
        path: "notifications/:id",
        element: <NotificationsPage />,
      },
      {
        path: "system/notification",
        element: <NotificationManagePage />,
      },
      {
        path: "account/profile",
        element: <AccountProfilePage />,
      },
      {
        path: "account/change-password",
        element: <ChangePasswordPage />,
      },
      {
        path: "*",
        element: <NotFoundPage />,
      },
    ],
  },
]);
