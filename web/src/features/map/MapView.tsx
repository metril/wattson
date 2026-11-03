import { useEffect, useRef, useState } from 'react';
import { loadMapLibre } from '@/lib/maplibre';
import type { components } from '@/lib/openapi';

export const STYLE_URL = 'https://tiles.openfreemap.org/styles/liberty';

export type GeoLine = components['schemas']['LineStringOut'];

export type MapApi = {
  setLine(l: GeoLine | null): void;
  fit(b: [number, number, number, number]): void;
  setMarker(ll: [number, number] | null): void;
  setWaypoints(w: [number, number][]): void;
  setTiles(fc: GeoJSON.FeatureCollection | null, onTileClick?: (name: string) => void): void;
  /** Current map centre as [lon, lat]. */
  getCenter(): [number, number];
};

const EMPTY: GeoJSON.FeatureCollection = { type: 'FeatureCollection', features: [] };

// Shown when the hosted style cannot be fetched (offline, blocked): the route still draws.
const BLANK_STYLE = { version: 8 as const, sources: {}, layers: [] };

const token = (name: string, fallback: string) =>
  getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;

/**
 * MapLibre map on the OpenFreeMap style. The library and its stylesheet load
 * on demand so they stay out of the entry bundle. Calls made through the api
 * before the style is ready are remembered and applied when it is.
 */
export default function MapView(p: {
  center?: [number, number];
  zoom?: number;
  onReady(api: MapApi): void;
  onClick?(ll: [number, number]): void;
  className?: string;
  'data-testid'?: string;
}) {
  const box = useRef<HTMLDivElement>(null);
  const cb = useRef(p);
  cb.current = p;
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let dead = false;
    let cleanup = () => {};
    (async () => {
      let ml: typeof import('maplibre-gl');
      try {
        ml = await loadMapLibre();
      } catch {
        if (!dead) setFailed(true);
        return;
      }
      if (dead || !box.current) return;

      let map: import('maplibre-gl').Map;
      try {
        map = new ml.Map({
          container: box.current,
          style: STYLE_URL,
          center: cb.current.center ?? [0, 30],
          zoom: cb.current.zoom ?? 1.5,
          attributionControl: false,
        });
      } catch {
        if (!dead) setFailed(true); // no WebGL
        return;
      }
      map.addControl(new ml.NavigationControl({ showCompass: false }), 'top-right');

      // Desired state; (re)applied whenever a style finishes loading.
      let line: GeoLine | null = null;
      let waypoints: [number, number][] = [];
      let tiles: GeoJSON.FeatureCollection | null = null;
      let onTile: ((name: string) => void) | undefined;
      let ready = false;
      let fellBack = false;

      const setData = (id: string, data: GeoJSON.GeoJSON) => {
        (map.getSource(id) as import('maplibre-gl').GeoJSONSource | undefined)?.setData(data);
      };
      const lineFC = (): GeoJSON.FeatureCollection =>
        line ? { type: 'FeatureCollection', features: [{ type: 'Feature', properties: {}, geometry: line }] } : EMPTY;
      const wpFC = (): GeoJSON.FeatureCollection => ({
        type: 'FeatureCollection',
        features: waypoints.map((c, i) => ({
          type: 'Feature',
          properties: { n: i + 1 },
          geometry: { type: 'Point', coordinates: c },
        })),
      });

      const setup = () => {
        const accent = token('--accent', '#54a9ff');
        const ground = token('--ground', '#0a1215');
        const chalk = token('--chalk', '#eaf1ee');
        map.addSource('tiles', { type: 'geojson', data: tiles ?? EMPTY });
        map.addLayer({ id: 'tiles-fill', type: 'fill', source: 'tiles', paint: { 'fill-color': ['coalesce', ['get', 'color'], accent], 'fill-opacity': ['coalesce', ['get', 'opacity'], 0.12] } });
        map.addLayer({ id: 'tiles-line', type: 'line', source: 'tiles', paint: { 'line-color': accent, 'line-opacity': 0.5, 'line-width': 1 } });
        map.addSource('route', { type: 'geojson', data: lineFC() });
        map.addLayer({ id: 'route-casing', type: 'line', source: 'route', layout: { 'line-cap': 'round', 'line-join': 'round' }, paint: { 'line-color': ground, 'line-width': 8, 'line-opacity': 0.85 } });
        map.addLayer({ id: 'route-line', type: 'line', source: 'route', layout: { 'line-cap': 'round', 'line-join': 'round' }, paint: { 'line-color': accent, 'line-width': 4.5 } });
        map.addSource('waypoints', { type: 'geojson', data: wpFC() });
        map.addLayer({ id: 'waypoints', type: 'circle', source: 'waypoints', paint: { 'circle-radius': 8, 'circle-color': accent, 'circle-stroke-color': chalk, 'circle-stroke-width': 2 } });
        ready = true;
      };

      map.on('style.load', setup);
      map.on('error', (e) => {
        // The hosted style failed before it loaded: carry on with a blank ground.
        if (!ready && !fellBack) {
          fellBack = true;
          map.setStyle(BLANK_STYLE);
        } else if (ready) {
          console.warn('MapLibre error', (e as { error?: unknown } | undefined)?.error ?? e);
        }
      });
      map.on('click', (e) => {
        if (map.getLayer('tiles-fill') && onTile) {
          const hit = map.queryRenderedFeatures(e.point, { layers: ['tiles-fill'] })[0];
          const name = hit?.properties?.name;
          if (typeof name === 'string') {
            onTile(name);
            return;
          }
        }
        cb.current.onClick?.([e.lngLat.lng, e.lngLat.lat]);
      });

      let marker: import('maplibre-gl').Marker | null = null;
      const api: MapApi = {
        setLine(l) {
          line = l;
          if (ready) setData('route', lineFC());
        },
        fit(b) {
          map.resize(); // the container may have just changed size
          map.fitBounds(
            [
              [b[0], b[1]],
              [b[2], b[3]],
            ],
            { padding: 40, duration: 0, maxZoom: 16 },
          );
        },
        setMarker(ll) {
          if (!ll) {
            marker?.remove();
            marker = null;
            return;
          }
          if (!marker) {
            const el = document.createElement('div');
            el.style.cssText = `width:18px;height:18px;border-radius:50%;background:${token('--accent', '#54a9ff')};border:3px solid ${token('--chalk', '#eaf1ee')};box-shadow:0 0 0 4px rgba(84,169,255,.35)`;
            marker = new ml.Marker({ element: el });
            // MapLibre makes a marker a focusable button; this one only shows where the rider is.
            el.setAttribute('role', 'img');
            el.setAttribute('aria-hidden', 'true');
            el.removeAttribute('tabindex');
            marker.setLngLat(ll).addTo(map);
          } else marker.setLngLat(ll);
        },
        setWaypoints(w) {
          waypoints = w;
          if (ready) setData('waypoints', wpFC());
        },
        getCenter() {
          const c = map.getCenter();
          return [c.lng, c.lat];
        },
        setTiles(fc, onTileClick) {
          tiles = fc;
          onTile = onTileClick;
          if (ready) setData('tiles', fc ?? EMPTY);
        },
      };
      // Panels that resize (view swap, split layout) must not leave a stretched or clipped canvas.
      const ro = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(() => map.resize());
      ro?.observe(box.current);
      cleanup = () => {
        ro?.disconnect();
        marker?.remove();
        map.remove();
      };
      cb.current.onReady(api);
    })();
    return () => {
      dead = true;
      cleanup();
    };
  }, []);

  if (failed) {
    return (
      <div
        role="status"
        data-testid={p['data-testid']}
        className={`flex flex-col items-center justify-center gap-1 rounded-panel border border-line bg-panel p-6 text-center text-dim ${p.className ?? ''}`}
      >
        <p className="font-medium text-chalk">Map unavailable</p>
        <p className="text-sm">This browser could not start WebGL or load the map. The route still works without it.</p>
      </div>
    );
  }
  return (
    <div
      ref={box}
      role="region"
      aria-label="Map"
      data-testid={p['data-testid']}
      className={`overflow-hidden rounded-panel border border-line bg-panel ${p.className ?? 'h-80'}`}
    />
  );
}
