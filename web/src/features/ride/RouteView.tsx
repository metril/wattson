import { lazy, Suspense, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useQuery } from '@tanstack/react-query';
import { Banner } from '@/components/Banner';
import { api, unwrap } from '@/lib/api';
import { positionAt } from '@/lib/route-math';
import { useMedia } from '@/lib/use-media';
import type { TickStore } from '@/lib/tick-store';
import {
  detectTier,
  effectiveView,
  hasSavedTier,
  isCombined,
  loadPrefs,
  savePrefs,
  type Caps,
  type ViewMode,
} from '@/lib/view-prefs';
import type { MapApi } from '@/features/map/MapView';
import { ProfileCanvas } from '@/features/routes/ProfileCanvas';
import { photosKey, profileKey, routeKey } from '@/features/routes/queries';
import type { components } from '@/lib/openapi';
import { VIEW_SLOT_ID } from './BikeBar';
import { ViewMenu } from './ViewMenu';

type PhotosOut = components['schemas']['RoutePhotosOut'];

// MapLibre must stay out of the entry bundle; the ride screen is eager.
const MapView = lazy(() => import('@/features/map/MapView'));
const Ride3D = lazy(() => import('./Ride3D'));
const PhotoView = lazy(() => import('./PhotoView'));

const FALLBACK_NOTE = {
  slow: '3D was too slow on this display, so the ride switched to 2D. Choose 3D in View to try again.',
  context_lost: 'The 3D view stopped responding, so the ride switched to 2D.',
  webgl: "3D isn't supported on this display, so the ride switched to 2D.",
} as const;

function photoNote(d: PhotosOut | undefined): string | null {
  switch (d?.status) {
    case 'building':
      return 'Finding street photos…';
    case 'ready':
      return d.photos.length === 0 ? 'No street photos along this route' : null;
    case 'error':
    case 'unavailable':
      return d.message || 'Street photos are unavailable right now';
    case 'disabled':
      return 'Add a Mapillary token in Settings to see street photos';
    default:
      return null;
  }
}

const INSET_CLS =
  'absolute bottom-3 right-3 h-32 w-44 md:h-40 md:w-60 min-[1600px]:h-56 min-[1600px]:w-80';

/** Class of the photo or map panel: split halves, or the large/inset pair of "Photos + map". */
function panelCls(view: ViewMode, swapped: boolean, which: 'photo' | 'map'): string {
  if (view === 'split') return 'h-60 min-w-0 md:h-[var(--view-h)] md:min-h-56';
  const large = (which === 'map') === swapped;
  return large ? 'absolute inset-0' : `${INSET_CLS} ride-inset z-10 overflow-hidden`;
}

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
  const photoQuery = useQuery({
    queryKey: photosKey(routeId),
    enabled: hasMap,
    retry: false,
    staleTime: 60_000,
    // The server retries a failed build only when asked >=10 min after the failure, so keep asking.
    refetchInterval: (q) => (q.state.data?.status === 'building' ? 3000 : q.state.data?.status === 'error' ? 60_000 : false),
    refetchOnWindowFocus: (q) => q.state.data?.status === 'error',
    queryFn: async () => unwrap(await api.GET('/api/routes/{id}/mapillary', { params: { path: { id: routeId } } })),
  });

  const [prefs] = useState(loadPrefs);
  const [want, setWant] = useState<ViewMode>(prefs.view);
  const [exaggeration, setExaggeration] = useState(prefs.exaggeration);
  const [tier, setTier] = useState(() => (hasSavedTier() ? prefs.tier : detectTier(navigator)));
  const [note, setNote] = useState<string | null>(null);
  const [swapped, setSwapped] = useState(false); // combined views: the map is the large one
  const [webgl] = useState(probeWebgl);
  const reducedMotion = useMedia('(prefers-reduced-motion: reduce)');
  const tv = useMedia('(min-width: 1600px)');
  const rail = useMedia('(min-width: 900px) and (orientation: landscape)');
  const wide = useMedia('(min-width: 768px)'); // Tailwind md: the map panel is always shown
  // On a TV the View menu sits in the top bar (BikeBar's slot), leaving the whole left column to the view.
  const [slot, setSlot] = useState<HTMLElement | null>(null);
  useLayoutEffect(() => setSlot(document.getElementById(VIEW_SLOT_ID)), []);
  const photoData = photoQuery.data;
  const photoList = photoData?.status === 'ready' ? photoData.photos : [];
  const caps: Caps = {
    webgl,
    reducedMotion,
    photos: photoList.length > 0,
    map: hasMap,
    photoNote: photoNote(photoData),
  };
  const view = effectiveView(want, caps);
  // The 2D map's API is only valid while its MapView is mounted.
  const combined = isCombined(view);
  const mapShown = view === '2d' || (combined && (showMap || wide));
  const map = mapShown ? mapApi : null;

  useEffect(() => {
    setMap(null); // a new MapView mounts (or none): drop the old api until it reports ready
  }, [view, mapShown]);

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
      savePrefs({ tier: t });
    }
  };
  const setExag = (x: number) => {
    setExaggeration(x);
    savePrefs({ exaggeration: x });
  };
  const fellBack = useRef((reason: 'slow' | 'context_lost' | 'webgl') => {
    savePrefs({ view: '2d' });
    setWant('2d');
    setNote(FALLBACK_NOTE[reason]);
  }).current;

  useEffect(() => {
    if (!map || !r) return;
    map.setLine(r.geojson);
    if (r.bbox.length === 4) map.fit(r.bbox as [number, number, number, number]);
  }, [map, r, swapped]);

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

  const viewMenu = (
    <ViewMenu value={view} caps={caps} exaggeration={exaggeration} onChange={choose} onExaggeration={setExag} />
  );

  if (route.isPending && profile.isPending) return null;
  if (profile.isError) {
    return <Banner tone="warn">This route is no longer available. Live metrics continue without the map.</Banner>;
  }

  return (
    <section aria-label="Route" className="flex flex-col gap-3">
      {note && <Banner tone="info">{note}</Banner>}
      {hasMap && tv && slot && createPortal(viewMenu, slot)}
      {hasMap && !(tv && slot) && (
        <div className="flex flex-wrap items-start gap-3">
          <button
            type="button"
            data-nav
            aria-pressed={showMap}
            onClick={() => setShowMap((v) => !v)}
            className="inline-flex min-h-touch items-center justify-center self-start rounded-control border border-line bg-raised px-4 text-chalk md:hidden"
          >
            {showMap ? 'Hide map' : 'Map'}
          </button>
          {viewMenu}
        </div>
      )}
      <div className="grid gap-3">
        {hasMap && (
          <div ref={wrap} data-testid="route-map" className={showMap ? 'block' : 'hidden md:block'}>
            <Suspense fallback={<div className="h-64 rounded-panel border border-line bg-panel" />}>
              {combined ? (
                showMap || wide ? (
                  <div
                    className={
                      view === 'split'
                        ? 'grid gap-3 md:grid-cols-2'
                        : 'ride-viewbox relative'
                    }
                  >
                    <div className={panelCls(view, swapped, 'photo')}>
                      <PhotoView photos={photoList} ticks={ticks} reducedMotion={reducedMotion} fill />
                    </div>
                    <div className={panelCls(view, swapped, 'map')}>
                      <MapView className="size-full" onReady={setMap} />
                    </div>
                    {view === 'photo-inset' && (
                      <button
                        type="button"
                        data-nav
                        data-testid="inset-swap"
                        aria-label={swapped ? 'Show street photo large' : 'Show map large'}
                        onClick={() => setSwapped((s) => !s)}
                        className={`${INSET_CLS} z-20 rounded-panel border-2 border-chalk/60 bg-transparent`}
                      />
                    )}
                  </div>
                ) : null
              ) : view === 'photo' ? (
                showMap || wide ? (
                  <PhotoView photos={photoList} ticks={ticks} reducedMotion={reducedMotion} />
                ) : null
              ) : view === '3d' ? (
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
                <MapView className="ride-viewbox" onReady={setMap} />
              )}
            </Suspense>
          </div>
        )}
        {prof && <ProfileCanvas profile={prof} cursor={ticks} height={tv ? 96 : rail ? 180 : hasMap ? 220 : 160} />}
      </div>
    </section>
  );
}
