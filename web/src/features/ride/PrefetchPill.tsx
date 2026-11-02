import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import { profileKey, routeKey } from '@/features/routes/queries';
import { routeUrls, vectorTemplate } from '@/sw/corridor';
import { hasController, prefetchRoute } from '@/sw/register';

const FRESH_MS = 7 * 864e5;
const HIDE_MS = 5000;

const storageKey = (id: number) => `wattson.prefetched.${id}`;

function savedRecently(id: number): boolean {
  try {
    const t = Date.parse(localStorage.getItem(storageKey(id)) ?? '');
    return Number.isFinite(t) && Date.now() - t < FRESH_MS;
  } catch {
    return false;
  }
}

function markSaved(id: number): void {
  try {
    localStorage.setItem(storageKey(id), new Date().toISOString());
  } catch {
    /* storage blocked: the map is simply fetched again next time */
  }
}

function saveData(): boolean {
  return !!(navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData;
}

type State = { kind: 'idle' } | { kind: 'saving'; pct: number } | { kind: 'saved' } | { kind: 'full' };

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
  const prof = profile.data;
  const hasMap = !!route.data && route.data.source !== 'synthetic';

  useEffect(() => {
    if (!hasMap || !prof || !hasController() || saveData() || savedRecently(routeId)) return;
    let live = true;
    void (async () => {
      try {
        const urls = routeUrls(prof, await vectorTemplate());
        if (!live || urls.length === 0) return;
        setState({ kind: 'saving', pct: 0 });
        const res = await prefetchRoute(urls, (done, total) => {
          if (live) setState({ kind: 'saving', pct: total ? Math.floor((done / total) * 100) : 0 });
        });
        if (!live) return;
        if (res.cached > 0 && res.failed === 0) markSaved(routeId);
        setState({ kind: 'saved' });
      } catch (e) {
        if (live) setState(e instanceof Error && e.message === 'quota' ? { kind: 'full' } : { kind: 'idle' });
      }
    })();
    return () => {
      live = false;
    };
  }, [hasMap, prof, routeId]);

  useEffect(() => {
    if (state.kind !== 'saved') return;
    const t = setTimeout(() => setState({ kind: 'idle' }), HIDE_MS);
    return () => clearTimeout(t);
  }, [state.kind]);

  if (state.kind === 'idle') return null;
  const text =
    state.kind === 'saving'
      ? `Saving map for offline… ${state.pct} %`
      : state.kind === 'saved'
        ? 'Map saved for this route'
        : "Couldn't save the map: storage is full";
  return (
    <p role="status" className="self-start rounded-control border border-line bg-raised px-4 py-2 text-sm text-chalk">
      {text}
    </p>
  );
}
