import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Banner } from '@/components/Banner';
import { api, unwrap } from '@/lib/api';
import { positionAt } from '@/lib/route-math';
import type { TickStore } from '@/lib/tick-store';
import type { MapApi } from '@/features/map/MapView';
import { ProfileCanvas } from '@/features/routes/ProfileCanvas';
import { profileKey, routeKey } from '@/features/routes/queries';

// MapLibre must stay out of the entry bundle; the ride screen is eager.
const MapView = lazy(() => import('@/features/map/MapView'));

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

  const [map, setMap] = useState<MapApi | null>(null);
  const [showMap, setShowMap] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);
  const r = route.data;
  const prof = profile.data;
  const hasMap = !!r && r.source !== 'synthetic';

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
      {hasMap && (
        <button
          type="button"
          aria-pressed={showMap}
          onClick={() => setShowMap((v) => !v)}
          className="inline-flex min-h-touch items-center justify-center self-start rounded-control border border-line bg-raised px-4 text-chalk md:hidden"
        >
          {showMap ? 'Hide map' : 'Map'}
        </button>
      )}
      <div className={hasMap ? 'grid gap-3 md:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)] md:gap-4' : 'grid gap-3'}>
        {hasMap && (
          <div ref={wrap} data-testid="route-map" className={showMap ? 'block' : 'hidden md:block'}>
            <Suspense fallback={<div className="h-64 rounded-panel border border-line bg-panel" />}>
              <MapView className="h-64 md:h-72 min-[1600px]:h-96" onReady={setMap} />
            </Suspense>
          </div>
        )}
        {prof && <ProfileCanvas profile={prof} cursor={ticks} height={hasMap ? 220 : 160} />}
      </div>
    </section>
  );
}
