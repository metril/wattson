import type { ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Navigate, useLocation } from 'react-router';
import { Banner } from '@/components/Banner';
import { api, dropKioskToken, getKioskToken, unwrap, wasKioskDropped } from './api';
import type { components } from './openapi';

type Schemas = components['schemas'];

export const ME_KEY = ['me'] as const;
export const AUTH_INFO_KEY = ['auth', 'providers'] as const;

/** The signed-in caller, or null when anonymous (401). */
export function useMe() {
  return useQuery<Schemas['MeOut'] | null>({
    queryKey: ME_KEY,
    retry: false,
    staleTime: 30_000,
    queryFn: async () => {
      const r = await api.GET('/api/auth/me');
      if (r.response.status === 401) {
        // The server prefers a Bearer token over the cookie, so a revoked kiosk
        // token would block every sign-in. Drop it and send the display to pairing.
        if (getKioskToken()) dropKioskToken();
        return null;
      }
      return unwrap(r);
    },
  });
}

/** Sign-in options and whether first-run setup is still pending. */
export function useAuthInfo() {
  return useQuery<Schemas['ProvidersOut']>({
    queryKey: AUTH_INFO_KEY,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/auth/providers')),
  });
}

export function RequireAuth({ children, admin }: { children: ReactNode; admin?: boolean }) {
  const loc = useLocation();
  const me = useMe();
  const info = useAuthInfo();

  if (me.isPending || info.isPending) {
    return (
      <div role="status" className="p-8 text-dim">
        Loading…
      </div>
    );
  }
  if (me.isError || info.isError) {
    return (
      <div className="p-4">
        <Banner tone="error">Can't reach the Wattson server. Check the connection and reload.</Banner>
      </div>
    );
  }
  if (info.data?.needs_setup) return <Navigate to="/setup" replace />;
  if (!me.data) {
    if (wasKioskDropped()) return <Navigate to="/kiosk" replace />;
    const rt = encodeURIComponent(loc.pathname + loc.search);
    return <Navigate to={`/login?return_to=${rt}`} replace />;
  }
  if (admin && !me.data.user.is_admin) return <Navigate to="/" replace />;
  return <>{children}</>;
}
