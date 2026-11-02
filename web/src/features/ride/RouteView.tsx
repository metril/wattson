import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Banner } from '@/components/Banner';
import { api, unwrap } from '@/lib/api';
import { positionAt } from '@/lib/route-math';
import type { TickStore } from '@/lib/tick-store';
import {
  detectTier,
  effectiveView,
  hasSavedTier,
  loadPrefs,
  savePrefs,
  type Caps,
  type ViewMode,
} from '@/lib/view-prefs';
import type { MapApi } from '@/features/map/MapView';
import { ProfileCanvas } from '@/features/routes/ProfileCanvas';
import { profileKey, routeKey } from '@/features/routes/queries';
import { ViewMenu } from './ViewMenu';

// MapLibre must stay out of the entry bundle; the ride screen is eager.
const MapView = lazy(() => import('@/features/map/MapView'));
const Ride3D = lazy(() => import('./Ride3D'));

const FALLBACK_NOTE = {
  slow: '3D was too slow on this display, so the ride switched to 2D. Choose 3D in View to try again.',
  context_lost: 'The 3D view stopped responding, so the ride switched to 2D.',
  webgl: "3D isn't supported on this display, so the ride switched to 2D.",
} as const;

function probeWebgl(): boolean {
  try {
    const gl = document.createElement('canvas').getContext('webgl2');
    if (!gl) return false;
    gl.getExtension?.('WEBGL_lose_context')?.loseContext(); // free the probe's context slot
    return true;
  } catch {
    return false;
  }
}

/** Live match of a media query. */
function useMedia(query: string): boolean {
  const [on, setOn] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const mq = window.matchMedia(query);
    const fn = () => setOn(mq.matches);
    fn();
    mq.addEventListener('change', fn);
    return () => mq.removeEventListener('change', fn);
  }, [query]);
  return on;
}

/** The route id of the latest tick, as React state that changes only when the route does. */
export function useRouteId(ticks: TickStore): number {
  const [id, setId] = useState(() => ticks.latest?.route_id ?? 0);
  useEffect(() => {
    let last = -1;
    const apply = (rid: number) => {
      if (rid === last) return;
      last = rid;
      setId(rid);
    };
    if (ticks.latest) apply(ticks.latest.route_id ?? 0);
    return ticks.subscribe((t) => apply(t.route_id ?? 0));
  }, [ticks]);
  return id;
}

/**
 * Map and elevation profile of the route being ridden. The map marker and the
 * profile cursor follow the TickStore from requestAnimationFrame callbacks, so
 * ticks never re-render this component. A route deleted mid-ride degrades to
 * the live metrics alone.
 */
export function RouteView({ routeId, ticks }: { routeId: number; ticks: TickStore }) {
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

  const [mapApi, setMap] = useState<MapApi | null>(null);
  const [showMap, setShowMap] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);
  const r = route.data;
  const prof = profile.data;
  const hasMap = !!r && r.source !== 'synthetic';

  const [prefs] = useState(loadPrefs);
  const [want, setWant] = useState<ViewMode>(prefs.view);
  const [exaggeration, setExaggeration] = useState(prefs.exaggeration);
  const [tier, setTier] = useState(() => (hasSavedTier() ? prefs.tier : detectTier(navigator)));
  const [note, setNote] = useState<string | null>(null);
  const [webgl] = useState(probeWebgl);
  const reducedMotion = useMedia('(prefers-reduced-motion: reduce)');
  const wide = useMedia('(min-width: 768px)'); // Tailwind md: the map panel is always shown
  // Street photos arrive with PhotoView; until then the option reports no photos.
  const caps: Caps = { webgl, reducedMotion, photos: false, map: hasMap };
  const view = effectiveView(want, caps);
  // The 2D map's API is only valid while its MapView is mounted.
  const map = view === '2d' ? mapApi : null;

  useEffect(() => {
    if (view !== '2d') setMap(null);
  }, [view]);

  useEffect(() => {
    if (!note) return;
    const t = setTimeout(() => setNote(null), 10_000);
    return () => clearTimeout(t);
  }, [note]);

  const choose = (v: ViewMode) => {
    setWant(v);
    setNote(null);
    savePrefs({ view: v });
    if (v === '3d') {
      const t = detectTier(navigator);
      setTier(t);
      savePrefs({ fellBack: false, tier: t });
    }
  };
  const setExag = (x: number) => {
    setExaggeration(x);
    savePrefs({ exaggeration: x });
  };
  const fellBack = useRef((reason: 'slow' | 'context_lost' | 'webgl') => {
    savePrefs({ view: '2d', fellBack: true });
    setWant('2d');
    setNote(FALLBACK_NOTE[reason]);
  }).current;

  useEffect(() => {
    if (!map || !r) return;
    map.setLine(r.geojson);
    if (r.bbox.length === 4) map.fit(r.bbox as [number, number, number, number]);
  }, [map, r]);

  useEffect(() => {
    if (!map || !prof) return;
    let raf = 0;
    const draw = () => {
      raf = 0;
      const t = ticks.latest;
      if (!t) return;
      const ll = positionAt(prof, t.distance_m);
      map.setMarker(ll);
      if (wrap.current) wrap.current.dataset.pos = `${ll[1]},${ll[0]}`;
    };
    draw();
    const unsub = ticks.subscribe(() => {
      if (!raf) raf = requestAnimationFrame(draw);
    });
    return () => {
      unsub();
      if (raf) cancelAnimationFrame(raf);
    };
  }, [map, prof, ticks]);

  if (route.isPending && profile.isPending) return null;
  if (profile.isError) {
    return <Banner tone="warn">This route is no longer available. Live metrics continue without the map.</Banner>;
  }

  return (
    <section aria-label="Route" className="flex flex-col gap-3">
      {note && <Banner tone="info">{note}</Banner>}
      {hasMap && (
        <div className="flex flex-wrap items-start gap-3">
          <button
            type="button"
            aria-pressed={showMap}
            onClick={() => setShowMap((v) => !v)}
            className="inline-flex min-h-touch items-center justify-center self-start rounded-control border border-line bg-raised px-4 text-chalk md:hidden"
          >
            {showMap ? 'Hide map' : 'Map'}
          </button>
          <ViewMenu value={view} caps={caps} exaggeration={exaggeration} onChange={choose} onExaggeration={setExag} />
        </div>
      )}
      <div className={hasMap ? 'grid gap-3 md:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)] md:gap-4' : 'grid gap-3'}>
        {hasMap && (
          <div ref={wrap} data-testid="route-map" className={showMap ? 'block' : 'hidden md:block'}>
            <Suspense fallback={<div className="h-64 rounded-panel border border-line bg-panel" />}>
              {view === '3d' ? (
                // A flyover in a hidden phone panel would still run WebGL, so it mounts only when shown.
                r && prof && (showMap || wide) ? (
                  <Ride3D
                    profile={prof}
                    line={r.geojson}
                    ticks={ticks}
                    exaggeration={exaggeration}
                    tier={tier}
                    onFallback={fellBack}
                  />
                ) : null
              ) : (
                <MapView className="h-64 md:h-72 min-[1600px]:h-96" onReady={setMap} />
              )}
            </Suspense>
          </div>
        )}
        {prof && <ProfileCanvas profile={prof} cursor={ticks} height={hasMap ? 220 : 160} />}
      </div>
    </section>
  );
}
