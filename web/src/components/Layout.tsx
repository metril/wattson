import { useEffect, useRef, useState } from 'react';
import { Bike, Dumbbell, Ellipsis, History, KeyRound, LogOut, Route, Settings, Unlink, User, Users } from 'lucide-react';
import { Navigate, NavLink, Outlet, useLocation, useNavigate } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { api, setKioskToken, unwrap } from '@/lib/api';
import { ME_KEY, useMe } from '@/lib/auth';
import { useSpatialNav } from '@/lib/spatial-nav';

const link =
  'flex min-h-touch min-w-touch flex-1 flex-col items-center justify-center gap-0.5 rounded-control px-0.5 py-1 text-[11px] font-medium sm:px-3 sm:text-xs text-dim md:flex-none md:py-3 aria-[current=page]:bg-raised aria-[current=page]:text-chalk hover:text-chalk';

/**
 * App shell: bottom tab bar on phones, left rail from 768 px up.
 * Content scrolls; the nav stays put.
 */
export function Layout() {
  const me = useMe();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const loc = useLocation();
  const isAdmin = !!me.data?.user.is_admin;
  // A paired display (API token) sees the ride screen only.
  const kiosk = me.data?.via === 'token';

  const [unpairing, setUnpairing] = useState(false);

  // D-pad: arrows move focus among [data-nav] controls; Escape/Backspace go back.
  const shell = useRef<HTMLDivElement>(null);
  useSpatialNav(shell);

  function unpair() {
    setKioskToken(null);
    qc.setQueryData(ME_KEY, null);
    navigate('/kiosk', { replace: true });
  }

  const [signOutError, setSignOutError] = useState<string | null>(null);

  // "More" holds the rarely used items so the bottom bar fits a 360 px phone.
  const [more, setMore] = useState(false);
  const moreBox = useRef<HTMLDivElement>(null);
  const moreBtn = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!more) return;
    const away = (e: PointerEvent) => {
      if (!moreBox.current?.contains(e.target as Node)) setMore(false);
    };
    const esc = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      e.preventDefault(); // the menu used the key: no "back"
      setMore(false);
      moreBtn.current?.focus();
    };
    document.addEventListener('pointerdown', away);
    document.addEventListener('keydown', esc);
    return () => {
      document.removeEventListener('pointerdown', away);
      document.removeEventListener('keydown', esc);
    };
  }, [more]);
  useEffect(() => setMore(false), [loc.pathname]);

  // Only leave once the server confirmed the session is gone; otherwise the
  // session stays valid and the user must know they are still signed in.
  async function signOut() {
    setMore(false);
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

  // A paired display shows the ride screen and the ride history (its FIT download needs the token).
  const kioskOk = loc.pathname === '/' || loc.pathname === '/history' || /^\/history\/\d+$/.test(loc.pathname);
  if (kiosk && !kioskOk) return <Navigate to="/" replace />;

  return (
    <div ref={shell} className="app-shell flex min-h-dvh flex-col md:flex-row">
      <nav
        aria-label="Main"
        className="fixed inset-x-0 bottom-0 z-30 flex gap-0.5 border-t border-line bg-panel p-0.5 sm:gap-1 sm:p-1 md:static md:inset-auto md:w-24 md:shrink-0 md:flex-col md:border-t-0 md:border-r md:p-2 pb-[max(0.25rem,env(safe-area-inset-bottom))] md:pb-2"
      >
        <span className="numeral hidden px-1 pt-2 pb-4 text-center text-lg tracking-wide md:block">
          Wattson
        </span>
        <NavLink to="/" end data-nav className={link}>
          <Bike aria-hidden className="size-6" />
          Ride
        </NavLink>
        <NavLink to="/history" data-nav className={link}>
          <History aria-hidden className="size-6" />
          History
        </NavLink>
        {kiosk ? (
          <button type="button" data-nav onClick={() => setUnpairing(true)} className={`${link} md:mt-auto`}>
            <Unlink aria-hidden className="size-6" />
            Unpair
          </button>
        ) : (
          <>
            <NavLink to="/routes" data-nav className={link}>
              <Route aria-hidden className="size-6" />
              Routes
            </NavLink>
            <NavLink to="/workouts" data-nav className={link}>
              <Dumbbell aria-hidden className="size-6" />
              Workouts
            </NavLink>
            <NavLink to="/riders" data-nav className={link}>
              <Users aria-hidden className="size-6" />
              Riders
            </NavLink>
            {isAdmin && (
              <NavLink to="/settings/general" data-nav className={link}>
                <Settings aria-hidden className="size-6" />
                Settings
              </NavLink>
            )}
            <div ref={moreBox} className="relative flex flex-1 md:mt-auto md:flex-none">
              <button
                ref={moreBtn}
                type="button"
                data-nav
                aria-expanded={more}
                aria-controls="more-menu"
                onClick={() => setMore((v) => !v)}
                className={`${link} w-full ${more || (loc.pathname === '/settings/tokens' || loc.pathname === '/account') ? 'bg-raised text-chalk' : ''}`}
              >
                <Ellipsis aria-hidden className="size-6" />
                More
              </button>
              {more && (
                <div
                  id="more-menu"
                  className="absolute right-0 bottom-full z-20 mb-1 flex w-44 flex-col gap-1 rounded-panel border border-line bg-panel p-1 shadow-lg md:bottom-0 md:left-full md:mb-0 md:ml-2 md:right-auto"
                >
                  <NavLink
                    to="/account"
                    data-nav
                    className="flex min-h-touch items-center gap-3 rounded-control px-3 font-medium text-chalk hover:bg-raised aria-[current=page]:bg-raised"
                  >
                    <User aria-hidden className="size-5" />
                    Account
                  </NavLink>
                  <NavLink
                    to="/settings/tokens"
                    data-nav
                    className="flex min-h-touch items-center gap-3 rounded-control px-3 font-medium text-chalk hover:bg-raised aria-[current=page]:bg-raised"
                  >
                    <KeyRound aria-hidden className="size-5" />
                    Tokens
                  </NavLink>
                  <button
                    type="button"
                    data-nav
                    onClick={signOut}
                    className="flex min-h-touch items-center gap-3 rounded-control px-3 text-left font-medium text-chalk hover:bg-raised"
                  >
                    <LogOut aria-hidden className="size-5" />
                    Sign out
                  </button>
                </div>
              )}
            </div>
          </>
        )}
      </nav>
      <main className="min-w-0 flex-1 p-4 pb-24 md:p-8 md:pb-8">
        {unpairing && (
          <div className="mx-auto mb-4 max-w-3xl">
            <Banner
              tone="warn"
              action={
                <div className="flex gap-2">
                  <Button variant="danger" onClick={unpair}>
                    Confirm unpair
                  </Button>
                  <Button onClick={() => setUnpairing(false)}>Cancel</Button>
                </div>
              }
            >
              This display forgets its token. The token stays valid until you revoke it in Settings → Tokens.
            </Banner>
          </div>
        )}
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
