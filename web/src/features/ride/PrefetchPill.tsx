import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import { profileKey, routeKey } from '@/features/routes/queries';
import { routeUrls, vectorTemplate } from '@/sw/corridor';
import { hasController, onControllerChange, prefetchRoute } from '@/sw/register';

const FRESH_MS = 7 * 864e5;
const RETRY_MS = 864e5; // after a failed or partial save, try again after a day, not every visit
const TOLERATED = 0.02; // share of tiles that may fail and still count as saved
const HIDE_MS = 5000;
const HIDE_BAD_MS = 10_000;

const savedKey = (id: number) => `wattson.prefetched.${id}`;
const retryKey = (id: number) => `wattson.prefetch_retry.${id}`;

function within(key: string, ms: number): boolean {
  try {
    const t = Date.parse(localStorage.getItem(key) ?? '');
    return Number.isFinite(t) && Date.now() - t < ms;
  } catch {
    return false;
  }
}

const skipForNow = (id: number) => within(savedKey(id), FRESH_MS) || within(retryKey(id), RETRY_MS);

function mark(key: string): void {
  try {
    localStorage.setItem(key, new Date().toISOString());
  } catch {
    /* storage blocked: the map is simply fetched again next time */
  }
}

function saveData(): boolean {
  return !!(navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData;
}

type State =
  | { kind: 'idle' }
  | { kind: 'saving'; pct: number }
  | { kind: 'saved' }
  | { kind: 'partial'; cached: number; total: number }
  | { kind: 'failed' }
  | { kind: 'full' };

/**
 * Downloads the map tiles along a route into the service worker cache so the
 * ride still renders if the internet drops. Quiet unless it has something to say.
 */
export function PrefetchPill({ routeId }: { routeId: number }) {
  const route = useQuery({
    queryKey: routeKey(routeId),
    staleTime: Infinity,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/routes/{id}', { params: { path: { id: routeId } } })),
  });
  const profile = useQuery({
    queryKey: profileKey(routeId),
    staleTime: Infinity,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/routes/{id}/profile', { params: { path: { id: routeId } } })),
  });
  const [state, setState] = useState<State>({ kind: 'idle' });
  // The worker takes control after the first visit's load; start once it does.
  const [controlled, setControlled] = useState(hasController);
  useEffect(() => {
    setControlled(hasController());
    return onControllerChange(() => setControlled(hasController()));
  }, []);
  const prof = profile.data;
  const hasMap = !!route.data && route.data.source !== 'synthetic';

  useEffect(() => {
    if (!hasMap || !prof || !controlled || saveData() || skipForNow(routeId)) return;
    const ctl = new AbortController(); // aborting tells the worker to stop fetching
    const live = () => !ctl.signal.aborted;
    void (async () => {
      try {
        const urls = routeUrls(prof, await vectorTemplate());
        if (!live() || urls.length === 0) return;
        setState({ kind: 'saving', pct: 0 });
        const res = await prefetchRoute(
          urls,
          (done, total) => {
            if (live()) setState({ kind: 'saving', pct: total ? Math.floor((done / total) * 100) : 0 });
          },
          ctl.signal,
        );
        if (!live()) return;
        const total = res.total || res.cached + res.failed;
        if (res.cached > 0 && res.failed <= total * TOLERATED) {
          mark(savedKey(routeId));
          setState({ kind: 'saved' });
        } else {
          mark(retryKey(routeId));
          setState(res.cached > 0 ? { kind: 'partial', cached: res.cached, total } : { kind: 'failed' });
        }
      } catch (e) {
        if (!live()) return;
        setState(e instanceof Error && e.message === 'quota' ? { kind: 'full' } : { kind: 'idle' });
      }
    })();
    return () => ctl.abort();
  }, [hasMap, prof, routeId, controlled]);

  useEffect(() => {
    if (state.kind !== 'saved' && state.kind !== 'partial' && state.kind !== 'failed') return;
    const t = setTimeout(() => setState({ kind: 'idle' }), state.kind === 'saved' ? HIDE_MS : HIDE_BAD_MS);
    return () => clearTimeout(t);
  }, [state.kind]);

  if (state.kind === 'idle') return null;
  const text =
    state.kind === 'saving'
      ? `Saving map for offline… ${state.pct} %`
      : state.kind === 'saved'
        ? 'Map saved for this route'
        : state.kind === 'partial'
          ? `Map partly saved (${state.cached} of ${state.total})`
          : state.kind === 'failed'
            ? "Couldn't save the map — you'll need internet"
            : "Couldn't save the map: storage is full";
  return (
    <p role="status" className="self-start rounded-control border border-line bg-raised px-4 py-2 text-sm text-chalk">
      {text}
    </p>
  );
}
