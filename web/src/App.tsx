import { lazy, Suspense, useEffect } from 'react';
import { Navigate, Route, Routes, useNavigate } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { Layout } from '@/components/Layout';
import { RequireAuth, ME_KEY } from '@/lib/auth';
import { dropKioskToken, getKioskToken, setUnauthorizedHandler } from '@/lib/api';
import { KioskPage } from '@/pages/Kiosk';
import { LoginPage } from '@/pages/Login';
import { SetupPage } from '@/pages/Setup';
import { RidersPage } from '@/pages/Riders';
import { RidePage } from '@/pages/Ride';
import { TokensPage } from '@/pages/settings/Tokens';
import { SettingsLayout } from '@/pages/settings/SettingsLayout';
import { GeneralForm } from '@/pages/settings/General';
import { BikeForm } from '@/pages/settings/Bike';
import { IntegrationsForm } from '@/pages/settings/Integrations';
import { AuthPage } from '@/pages/settings/Auth';
import { UsersPage } from '@/pages/settings/Users';

// Route pages (and MapLibre behind them) load on demand to keep the entry bundle small.
const RoutesPage = lazy(() => import('@/pages/Routes').then((m) => ({ default: m.RoutesPage })));
const RouteDetailPage = lazy(() => import('@/pages/RouteDetail').then((m) => ({ default: m.RouteDetailPage })));
const RouteSyntheticPage = lazy(() =>
  import('@/pages/RouteSynthetic').then((m) => ({ default: m.RouteSyntheticPage })),
);

const RouteDrawPage = lazy(() => import('@/pages/RouteDraw').then((m) => ({ default: m.RouteDrawPage })));

const lazyPage = (el: JSX.Element) => (
  <Suspense
    fallback={
      <p role="status" className="text-dim">
        Loading…
      </p>
    }
  >
    {el}
  </Suspense>
);

export function AppRoutes() {
  const qc = useQueryClient();
  const navigate = useNavigate();

  // Any 401 outside login/me drops the cached session and returns to sign-in.
  useEffect(() => {
    setUnauthorizedHandler((returnTo) => {
      qc.setQueryData(ME_KEY, null);
      // A paired display whose token was revoked goes back to the pairing hint.
      if (getKioskToken()) {
        dropKioskToken();
        navigate('/kiosk', { replace: true });
        return;
      }
      navigate(`/login?return_to=${encodeURIComponent(returnTo)}`, { replace: true });
    });
    return () => setUnauthorizedHandler(() => {});
  }, [qc, navigate]);

  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/setup" element={<SetupPage />} />
      <Route path="/kiosk" element={<KioskPage />} />
      <Route
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<RidePage />} />
        <Route path="riders" element={<RidersPage />} />
        <Route path="routes" element={lazyPage(<RoutesPage />)} />
        <Route path="routes/new/synthetic" element={lazyPage(<RouteSyntheticPage />)} />
        <Route path="routes/new/draw" element={lazyPage(<RouteDrawPage />)} />
        <Route path="routes/:id" element={lazyPage(<RouteDetailPage />)} />
        <Route path="settings/tokens" element={<TokensPage />} />
        <Route
          path="settings"
          element={
            <RequireAuth admin>
              <SettingsLayout />
            </RequireAuth>
          }
        >
          <Route index element={<Navigate to="general" replace />} />
          <Route path="general" element={<GeneralForm />} />
          <Route path="bike" element={<BikeForm />} />
          <Route path="integrations" element={<IntegrationsForm />} />
          <Route path="auth" element={<AuthPage />} />
          <Route path="users" element={<UsersPage />} />
          <Route path="*" element={<Navigate to="general" replace />} />
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
