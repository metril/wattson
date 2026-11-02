import { useEffect } from 'react';
import { Navigate, Route, Routes, useNavigate } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { Layout } from '@/components/Layout';
import { Card } from '@/components/Card';
import { RequireAuth, ME_KEY } from '@/lib/auth';
import { getKioskToken, setKioskToken, setUnauthorizedHandler } from '@/lib/api';
import { KioskPage } from '@/pages/Kiosk';
import { TokensPage } from '@/pages/settings/Tokens';
import { SettingsLayout } from '@/pages/settings/SettingsLayout';
import { GeneralForm } from '@/pages/settings/General';
import { BikeForm } from '@/pages/settings/Bike';
import { IntegrationsForm } from '@/pages/settings/Integrations';
import { AuthPage } from '@/pages/settings/Auth';
import { UsersPage } from '@/pages/settings/Users';

// Screens arrive in later tasks; each route renders a titled placeholder.
function Page({ title }: { title: string }) {
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4">
      <h1 className="numeral text-2xl">{title}</h1>
      <Card className="text-dim">Coming soon.</Card>
    </div>
  );
}

function Standalone({ title }: { title: string }) {
  return (
    <div className="mx-auto flex min-h-dvh max-w-md flex-col justify-center gap-4 p-4">
      <h1 className="numeral text-2xl">{title}</h1>
      <Card className="text-dim">Coming soon.</Card>
    </div>
  );
}

export function AppRoutes() {
  const qc = useQueryClient();
  const navigate = useNavigate();

  // Any 401 outside login/me drops the cached session and returns to sign-in.
  useEffect(() => {
    setUnauthorizedHandler((returnTo) => {
      qc.setQueryData(ME_KEY, null);
      // A paired display whose token was revoked goes back to the pairing hint.
      if (getKioskToken()) {
        setKioskToken(null);
        navigate('/kiosk', { replace: true });
        return;
      }
      navigate(`/login?return_to=${encodeURIComponent(returnTo)}`, { replace: true });
    });
    return () => setUnauthorizedHandler(() => {});
  }, [qc, navigate]);

  return (
    <Routes>
      <Route path="/login" element={<Standalone title="Sign in" />} />
      <Route path="/setup" element={<Standalone title="Set up Wattson" />} />
      <Route path="/kiosk" element={<KioskPage />} />
      <Route
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<Page title="Ride" />} />
        <Route path="riders" element={<Page title="Riders" />} />
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
