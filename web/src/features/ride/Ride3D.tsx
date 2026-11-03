import { useEffect, useRef } from 'react';
import { STYLE_URL, type GeoLine } from '@/features/map/MapView';
import { extrapolate, routeBearing, smoothBearing } from '@/lib/camera';
import { FpsMonitor, type FpsOpts } from '@/lib/fps-monitor';
import { loadMapLibre } from '@/lib/maplibre';
import { DEM_MAX_ZOOM } from '@/sw/corridor';
import { positionAt, type Profile } from '@/lib/route-math';
import type { TickStore } from '@/lib/tick-store';
import { savePrefs, type Tier } from '@/lib/view-prefs';

export const DEM_URL = 'https://s3.amazonaws.com/elevation-tiles-prod/terrarium/{z}/{x}/{y}.png';
const BLANK_STYLE = { version: 8 as const, sources: {}, layers: [] };

type Ml = import('@/lib/maplibre').MapLibre;
type MlMap = import('maplibre-gl').Map;
type Marker = import('maplibre-gl').Marker;
export type FallbackReason = 'slow' | 'context_lost' | 'webgl';

/** An error event for the style document itself: not a tile (sourceId) and not another resource's URL. */
function isStyleFailure(e: unknown): boolean {
  const ev = e as { sourceId?: string; tile?: unknown; error?: { url?: unknown } } | undefined;
  if (ev?.sourceId || ev?.tile) return false;
  const url = ev?.error?.url;
  return url === undefined || url === STYLE_URL;
}

const token = (name: string, fallback: string) =>
  getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;

type Live = { map: MlMap; marker: Marker; ready: boolean; remove(): void };

/**
 * Terrain flyover: MapLibre with a Terrarium DEM, the camera following the
 * rider along the route. Everything per frame runs in requestAnimationFrame
 * against the TickStore; this component renders once. A slow display steps
 * down to a lite tier and then asks the parent to fall back to 2D.
 */
export default function Ride3D(p: {
  profile: Profile;
  line: GeoLine;
  ticks: TickStore;
  exaggeration: number;
  tier: Tier;
  onFallback(reason: FallbackReason): void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const cb = useRef(p);
  cb.current = p;
  const live = useRef<Live | null>(null);

  useEffect(() => {
    const l = live.current;
    if (l?.ready) l.map.setTerrain({ source: 'terrain', exaggeration: p.exaggeration });
  }, [p.exaggeration]);

  const { profile, line, ticks } = p;
  useEffect(() => {
    let dead = false;
    let raf = 0;
    let tier: Tier = cb.current.tier;
    // Test hook: end-to-end tests shorten the warm-up and windows (and raise the threshold) to see the fallback quickly.
    const monitor = new FpsMonitor((window as { __wattsonFps?: FpsOpts }).__wattsonFps);
    const lengthM = profile.length_m;

    let lastD = ticks.latest?.distance_m ?? 0;
    let speed = ticks.latest?.speed_kmh ?? 0;
    let fresh = true;
    let tickAt = 0;
    let bearing = routeBearing(profile, lastD);
    let prevNow = 0;
    const unsub = ticks.subscribe((t) => {
      lastD = t.distance_m;
      speed = t.speed_kmh;
      fresh = true;
    });

    const fail = (r: FallbackReason) => {
      if (!dead) cb.current.onFallback(r);
    };

    const build = (ml: Ml, t: Tier): Live | null => {
      if (!box.current) return null;
      let map: MlMap;
      try {
        map = new ml.Map({
          container: box.current,
          style: STYLE_URL,
          center: positionAt(profile, lastD),
          bearing,
          pitch: 62,
          maxPitch: 70,
          zoom: 15.5,
          pixelRatio: Math.min(window.devicePixelRatio || 1, t === 'lite' ? 1 : 1.5),
          interactive: false,
          fadeDuration: 0,
          canvasContextAttributes: { antialias: false },
          attributionControl: false,
        });
      } catch {
        fail('webgl');
        return null;
      }
      // Handle for end-to-end tests, which assert on the live camera (map.getPitch()), not on data attributes.
      (box.current as (HTMLDivElement & { __map?: MlMap }) | null)!.__map = map;
      const accent = token('--accent', '#54a9ff');
      const el = document.createElement('div');
      el.style.cssText = `width:18px;height:18px;border-radius:50%;background:${accent};border:3px solid ${token('--chalk', '#eaf1ee')};box-shadow:0 0 0 4px rgba(84,169,255,.35)`;
      const marker = new ml.Marker({ element: el }).setLngLat(positionAt(profile, lastD)).addTo(map);
      // MapLibre makes a marker a focusable button; this one only shows where the rider is.
      el.setAttribute('role', 'img');
      el.setAttribute('aria-hidden', 'true');
      el.removeAttribute('tabindex');
      const l: Live = {
        map,
        marker,
        ready: false,
        remove() {
          marker.remove();
          map.remove();
        },
      };

      let fellBackStyle = false;
      map.on('style.load', () => {
        map.addSource('terrain', {
          type: 'raster-dem',
          tiles: [DEM_URL],
          encoding: 'terrarium',
          tileSize: 256,
          maxzoom: t === 'lite' ? 11 : DEM_MAX_ZOOM,
        });
        map.setTerrain({ source: 'terrain', exaggeration: cb.current.exaggeration });
        map.addSource('route', { type: 'geojson', data: { type: 'Feature', properties: {}, geometry: line } });
        map.addLayer({ id: 'route-casing', type: 'line', source: 'route', layout: { 'line-cap': 'round', 'line-join': 'round' }, paint: { 'line-color': token('--ground', '#0a1215'), 'line-width': 9, 'line-opacity': 0.85 } });
        map.addLayer({ id: 'route-line', type: 'line', source: 'route', layout: { 'line-cap': 'round', 'line-join': 'round' }, paint: { 'line-color': accent, 'line-width': 5 } });
        if (t === 'lite') {
          for (const layer of map.getStyle()?.layers ?? []) {
            const heavy = layer.type === 'fill-extrusion' || (layer.type === 'symbol' && (layer.minzoom ?? 0) >= 15);
            if (heavy) map.setLayoutProperty(layer.id, 'visibility', 'none');
          }
        }
        l.ready = true;
        monitor.arm();
      });
      map.on('error', (e: unknown) => {
        // The hosted style failed before it loaded: terrain and route still work on a blank ground.
        // Sprite, glyph and tile failures (they carry their own URL or a source id) leave the style alone.
        if (!l.ready && !fellBackStyle && isStyleFailure(e)) {
          fellBackStyle = true;
          map.setStyle(BLANK_STYLE);
        }
      });
      map.on('webglcontextlost', () => fail('context_lost'));
      return l;
    };

    (async () => {
      let ml: Ml;
      try {
        ml = await loadMapLibre();
      } catch {
        fail('webgl');
        return;
      }
      if (dead) return;
      live.current = build(ml, tier);
      if (!live.current) return;

      monitor.onDegrade(() => {
        if (dead) return;
        if (tier === 'full') {
          tier = 'lite';
          savePrefs({ tier: 'lite' });
          live.current?.remove();
          live.current = build(ml, 'lite');
        } else {
          fail('slow');
        }
      });

      const frame = (now: number) => {
        raf = requestAnimationFrame(frame);
        const dt = prevNow ? Math.min((now - prevNow) / 1000, 1) : 0;
        prevNow = now;
        const cur = live.current;
        if (!cur?.ready) return;
        if (fresh) {
          fresh = false;
          tickAt = now;
        }
        const d = extrapolate(lastD, speed, (now - tickAt) / 1000, lengthM);
        bearing = smoothBearing(bearing, routeBearing(profile, d), dt);
        const ll = positionAt(profile, d);
        cur.map.jumpTo({ center: ll, bearing });
        cur.marker.setLngLat(ll);
        const el = box.current;
        if (el) {
          el.dataset.pitch = cur.map.getPitch().toFixed(1);
          el.dataset.bearing = bearing.toFixed(1);
        }
        monitor.frame(now);
      };
      raf = requestAnimationFrame(frame);
    })();

    return () => {
      dead = true;
      unsub();
      cancelAnimationFrame(raf);
      live.current?.remove();
      live.current = null;
    };
  }, [profile, line, ticks]);

  return (
    <div
      ref={box}
      role="region"
      aria-label="3D route view"
      data-testid="route-3d"
      className="ride-viewbox overflow-hidden rounded-panel border border-line bg-panel"
    />
  );
}
