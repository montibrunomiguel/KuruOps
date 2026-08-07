import { Navigate, Route, Routes } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "./auth/AuthContext";
import { LoginPage } from "./pages/Login";
import { ChangePasswordPage } from "./pages/ChangePassword";
import { ProfilePage } from "./pages/Profile";
import { ForgotPasswordPage } from "./pages/ForgotPassword";
import { ResetPasswordPage } from "./pages/ResetPassword";
import { Sidebar } from "./components/Sidebar";
import { SettingsLayout } from "./pages/settings/SettingsLayout";
import { DashboardLayout } from "./pages/dashboard/DashboardLayout";
import { AlertsListPage } from "./pages/alerts/AlertsListPage";
import { AlertDetailPage } from "./pages/alerts/AlertDetailPage";
import { IncidentsListPage } from "./pages/incidents/IncidentsListPage";
import { IncidentDetailPage } from "./pages/incidents/IncidentDetailPage";
import { PlaybooksListPage } from "./pages/playbooks/PlaybooksListPage";
import { PlaybookDetailPage } from "./pages/playbooks/PlaybookDetailPage";
import { CommandPalette } from "./components/CommandPalette";

function AppShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="app-shell">
      <Sidebar />
      <CommandPalette />
      <main className="content">{children}</main>
    </div>
  );
}

// RequireAuth also enforces the must-change-password gate here on the
// frontend -- purely a UX shortcut (skip straight to the right screen).
// The backend enforces the same thing for real: every /api/v1 route except
// change-password rejects a token with mustChangePassword set (see
// middleware.RequirePasswordChanged), so this can't be bypassed by editing
// the URL.
function RequireAuth({ children }: { children: React.ReactElement }) {
  const { isAuthenticated, mustChangePassword } = useAuth();
  if (!isAuthenticated) return <Navigate to="/login" replace />;
  if (mustChangePassword) return <Navigate to="/change-password" replace />;
  return <AppShell>{children}</AppShell>;
}

function RequireAdmin({ children }: { children: React.ReactElement }) {
  const { t } = useTranslation();
  const { isAdmin } = useAuth();
  if (!isAdmin) {
    return (
      <div className="panel">
        <p style={{ margin: 0 }}>{t("access.adminOnly")}</p>
      </div>
    );
  }
  return children;
}

// RequireResourceAccess mirrors the backend's middleware.RequireResourceAccess
// gate on the same capability set (alerts/incidents/followup) -- without
// this, a user lacking a capability could still type the URL directly and
// land on a page whose data fetch just 403s with a raw error banner instead
// of a clear "you don't have access" message.
function RequireResourceAccess({
  capability,
  children,
}: {
  capability: string;
  children: React.ReactElement;
}) {
  const { t } = useTranslation();
  const { hasResourceAccess } = useAuth();
  if (!hasResourceAccess(capability)) {
    return (
      <div className="panel">
        <p style={{ margin: 0 }}>{t("access.noAccess")}</p>
      </div>
    );
  }
  return children;
}

function ChangePasswordRoute() {
  const { isAuthenticated, mustChangePassword } = useAuth();
  if (!isAuthenticated) return <Navigate to="/login" replace />;
  if (!mustChangePassword) return <Navigate to="/dashboard" replace />;
  return <ChangePasswordPage />;
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/forgot-password" element={<ForgotPasswordPage />} />
      <Route path="/reset-password" element={<ResetPasswordPage />} />
      <Route path="/change-password" element={<ChangePasswordRoute />} />

      <Route path="/dashboard/*" element={<RequireAuth><DashboardLayout /></RequireAuth>} />

      <Route
        path="/alerts"
        element={
          <RequireAuth>
            <RequireResourceAccess capability="alerts">
              <AlertsListPage />
            </RequireResourceAccess>
          </RequireAuth>
        }
      />
      <Route
        path="/alerts/:id"
        element={
          <RequireAuth>
            <RequireResourceAccess capability="alerts">
              <AlertDetailPage />
            </RequireResourceAccess>
          </RequireAuth>
        }
      />

      <Route
        path="/incidents"
        element={
          <RequireAuth>
            <RequireResourceAccess capability="incidents">
              <IncidentsListPage />
            </RequireResourceAccess>
          </RequireAuth>
        }
      />
      <Route
        path="/incidents/:id"
        element={
          <RequireAuth>
            <RequireResourceAccess capability="incidents">
              <IncidentDetailPage />
            </RequireResourceAccess>
          </RequireAuth>
        }
      />

      <Route path="/playbooks" element={<RequireAuth><PlaybooksListPage /></RequireAuth>} />
      <Route path="/playbooks/:id" element={<RequireAuth><PlaybookDetailPage /></RequireAuth>} />

      <Route path="/profile" element={<RequireAuth><ProfilePage /></RequireAuth>} />

      <Route
        path="/settings/*"
        element={
          <RequireAuth>
            <RequireAdmin>
              <SettingsLayout />
            </RequireAdmin>
          </RequireAuth>
        }
      />

      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  );
}
