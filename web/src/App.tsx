import { useEffect } from 'react';
import { Navigate, Route, Routes, useNavigate } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { Layout } from '@/components/Layout';
import { Card } from '@/components/Card';
import { RequireAuth, ME_KEY } from '@/lib/auth';
import { setUnauthorizedHandler } from '@/lib/api';

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
      navigate(`/login?return_to=${encodeURIComponent(returnTo)}`, { replace: true });
    });
    return () => setUnauthorizedHandler(() => {});
  }, [qc, navigate]);

  return (
    <Routes>
      <Route path="/login" element={<Standalone title="Sign in" />} />
      <Route path="/setup" element={<Standalone title="Set up Wattson" />} />
      <Route path="/kiosk" element={<Standalone title="Kiosk" />} />
      <Route
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<Page title="Ride" />} />
        <Route path="riders" element={<Page title="Riders" />} />
        <Route path="settings/tokens" element={<Page title="API tokens" />} />
        <Route
          path="settings/*"
          element={
            <RequireAuth admin>
              <Page title="Settings" />
            </RequireAuth>
          }
        />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
