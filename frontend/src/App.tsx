import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "./auth/AuthContext";
import { Sidebar } from "./components/Sidebar";
import { CommandPalette } from "./components/CommandPalette";

// Lazy, one chunk per route instead of one 890KB+ bundle everyone downloads
// up front just to see the login screen -- each import() only fires the
// first time its route is actually visited. Sidebar/CommandPalette stay
// eager: they're part of AppShell, rendered on every authenticated route
// regardless of which page chunk is loading.
const LoginPage = lazy(() => import("./pages/Login").then((m) => ({ default: m.LoginPage })));
const ChangePasswordPage = lazy(() => import("./pages/ChangePassword").then((m) => ({ default: m.ChangePasswordPage })));
const ProfilePage = lazy(() => import("./pages/Profile").then((m) => ({ default: m.ProfilePage })));
const ForgotPasswordPage = lazy(() => import("./pages/ForgotPassword").then((m) => ({ default: m.ForgotPasswordPage })));
const ResetPasswordPage = lazy(() => import("./pages/ResetPassword").then((m) => ({ default: m.ResetPasswordPage })));
const SettingsLayout = lazy(() => import("./pages/settings/SettingsLayout").then((m) => ({ default: m.SettingsLayout })));
const DashboardLayout = lazy(() => import("./pages/dashboard/DashboardLayout").then((m) => ({ default: m.DashboardLayout })));
const AlertsListPage = lazy(() => import("./pages/alerts/AlertsListPage").then((m) => ({ default: m.AlertsListPage })));
const AlertDetailPage = lazy(() => import("./pages/alerts/AlertDetailPage").then((m) => ({ default: m.AlertDetailPage })));
const IncidentsListPage = lazy(() => import("./pages/incidents/IncidentsListPage").then((m) => ({ default: m.IncidentsListPage })));
const IncidentDetailPage = lazy(() => import("./pages/incidents/IncidentDetailPage").then((m) => ({ default: m.IncidentDetailPage })));
const PlaybooksListPage = lazy(() => import("./pages/playbooks/PlaybooksListPage").then((m) => ({ default: m.PlaybooksListPage })));
const PlaybookDetailPage = lazy(() => import("./pages/playbooks/PlaybookDetailPage").then((m) => ({ default: m.PlaybookDetailPage })));

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

// RouteFallback is deliberately minimal (no spinner component exists in
// this codebase yet) -- route chunks are small and cached after first
// visit, so this is only ever visible for a moment on a cold load.
function RouteFallback() {
  return <div className="empty-state">…</div>;
}

export default function App() {
  return (
    <Suspense fallback={<RouteFallback />}>
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
    </Suspense>
  );
}
