import { useState } from 'react';
import { Bike, KeyRound, LogOut, Settings, Users } from 'lucide-react';
import { NavLink, Outlet, useNavigate } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { Banner } from '@/components/Banner';
import { api, unwrap } from '@/lib/api';
import { ME_KEY, useMe } from '@/lib/auth';

const link =
  'flex min-h-touch min-w-touch flex-1 flex-col items-center justify-center gap-0.5 rounded-control px-3 py-1 text-xs font-medium text-dim md:flex-none md:py-3 aria-[current=page]:bg-raised aria-[current=page]:text-chalk hover:text-chalk';

/**
 * App shell: bottom tab bar on phones, left rail from 768 px up.
 * Content scrolls; the nav stays put.
 */
export function Layout() {
  const me = useMe();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const isAdmin = !!me.data?.user.is_admin;

  const [signOutError, setSignOutError] = useState<string | null>(null);

  // Only leave once the server confirmed the session is gone; otherwise the
  // session stays valid and the user must know they are still signed in.
  async function signOut() {
    setSignOutError(null);
    try {
      unwrap(await api.POST('/api/auth/logout'));
    } catch (e) {
      setSignOutError(
        e instanceof Error && e.name === 'ApiError'
          ? `Couldn't sign out: ${e.message}`
          : "Couldn't reach the server. You are still signed in.",
      );
      return;
    }
    qc.setQueryData(ME_KEY, null);
    navigate('/login', { replace: true });
  }

  return (
    <div className="flex min-h-dvh flex-col md:flex-row">
      <nav
        aria-label="Main"
        className="fixed inset-x-0 bottom-0 z-10 flex gap-1 border-t border-line bg-panel p-1 md:static md:inset-auto md:w-24 md:shrink-0 md:flex-col md:border-t-0 md:border-r md:p-2 pb-[max(0.25rem,env(safe-area-inset-bottom))] md:pb-2"
      >
        <span className="numeral hidden px-1 pt-2 pb-4 text-center text-lg tracking-wide md:block">
          Wattson
        </span>
        <NavLink to="/" end className={link}>
          <Bike aria-hidden className="size-6" />
          Ride
        </NavLink>
        <NavLink to="/riders" className={link}>
          <Users aria-hidden className="size-6" />
          Riders
        </NavLink>
        {isAdmin && (
          <NavLink to="/settings/general" className={link}>
            <Settings aria-hidden className="size-6" />
            Settings
          </NavLink>
        )}
        <NavLink to="/settings/tokens" className={link}>
          <KeyRound aria-hidden className="size-6" />
          Tokens
        </NavLink>
        <button type="button" onClick={signOut} className={`${link} md:mt-auto`}>
          <LogOut aria-hidden className="size-6" />
          Sign out
        </button>
      </nav>
      <main className="min-w-0 flex-1 p-4 pb-24 md:p-8 md:pb-8">
        {signOutError && (
          <div className="mx-auto mb-4 max-w-3xl">
            <Banner tone="error">{signOutError}</Banner>
          </div>
        )}
        <Outlet />
      </main>
    </div>
  );
}
