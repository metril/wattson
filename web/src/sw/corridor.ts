// Pure helpers shared by the page and the service worker (sw.ts). No DOM or
// worker types here: both tsconfigs compile this file.

export const CACHE = 'wattson-tiles-v1';
export const MAX_ENTRIES = 4000;
/** zoom → corridor buffer in metres */
export const VECTOR_ZOOMS: Record<number, number> = { 14: 300, 13: 1000, 12: 3000, 10: 10000 };
/** The highest DEM zoom the 3D view asks for (full tier): the corridor must prefetch it, or terrain is flat offline. */
export const DEM_MAX_ZOOM = 13;
export const DEM_ZOOMS: Record<number, number> = { 13: 500, 12: 500, 11: 2000, 10: 5000, 8: 20000 };
export const MAX_URLS = 1500;

const OPENFREEMAP = 'https://tiles.openfreemap.org';
export const DEM_TEMPLATE = 'https://s3.amazonaws.com/elevation-tiles-prod/terrarium/{z}/{x}/{y}.png';
const M_PER_DEG = 111_320;
const MAX_LAT = 85.0511;

export function lonLatToTile(lon: number, lat: number, z: number): [number, number] {
  const n = 2 ** z;
  const r = (Math.min(MAX_LAT, Math.max(-MAX_LAT, lat)) * Math.PI) / 180;
  const x = Math.floor(((lon + 180) / 360) * n);
  const y = Math.floor(((1 - Math.log(Math.tan(r) + 1 / Math.cos(r)) / Math.PI) / 2) * n);
  const clamp = (v: number) => Math.min(n - 1, Math.max(0, v));
  return [clamp(x), clamp(y)];
}

/**
 * Tiles within `zooms[z]` metres of the polyline at each zoom, as "z/x/y",
 * deduped and ordered by where along the route each one is first needed
 * (lower zooms first on ties). The buffer is a square around sample points, a
 * slight over-cover that keeps the maths simple.
 */
export function corridorTiles(lon: number[], lat: number[], zooms: Record<number, number>): string[] {
  const n = Math.min(lon.length, lat.length);
  if (n === 0) return [];
  const found = new Map<string, number>(); // tile → distance along route at first sight
  const zs = Object.keys(zooms).map(Number).sort((a, b) => a - b);
  const add = (z: number, buffer: number, lo: number, la: number, d: number) => {
    const dLat = buffer / M_PER_DEG;
    const dLon = buffer / (M_PER_DEG * Math.max(0.05, Math.cos((la * Math.PI) / 180)));
    const [x0, y0] = lonLatToTile(lo - dLon, la + dLat, z);
    const [x1, y1] = lonLatToTile(lo + dLon, la - dLat, z);
    for (let y = y0; y <= y1; y++) {
      for (let x = x0; x <= x1; x++) {
        const k = `${z}/${x}/${y}`;
        if (!found.has(k)) found.set(k, d);
      }
    }
  };
  let dist = 0;
  for (let i = 0; i < n; i++) {
    let segLen = 0;
    if (i > 0) {
      const dx = (lon[i] - lon[i - 1]) * M_PER_DEG * Math.cos((lat[i] * Math.PI) / 180);
      const dy = (lat[i] - lat[i - 1]) * M_PER_DEG;
      segLen = Math.hypot(dx, dy);
    }
    for (const z of zs) {
      const buffer = zooms[z];
      const step = Math.max(buffer / 2, 50);
      const parts = i === 0 ? 1 : Math.max(1, Math.ceil(segLen / step));
      for (let p = 1; p <= parts; p++) {
        const f = i === 0 ? 1 : p / parts;
        const lo = i === 0 ? lon[0] : lon[i - 1] + (lon[i] - lon[i - 1]) * f;
        const la = i === 0 ? lat[0] : lat[i - 1] + (lat[i] - lat[i - 1]) * f;
        add(z, buffer, lo, la, i === 0 ? 0 : dist + segLen * f);
      }
    }
    dist += segLen;
  }
  return [...found.entries()]
    .map(([k, d], i) => ({ k, d, i }))
    .sort((a, b) => a.d - b.d || a.i - b.i)
    .map((e) => e.k);
}

export function tileUrls(tiles: string[], template: string): string[] {
  return tiles.map((t) => {
    const [z, x, y] = t.split('/');
    return template.replace('{z}', z).replace('{x}', x).replace('{y}', y);
  });
}

/** Vector and DEM urls for a route, merged so both advance along the route together, capped. */
export function routeUrls(p: { lon: number[]; lat: number[] }, vectorTpl: string, max = MAX_URLS): string[] {
  const a = tileUrls(corridorTiles(p.lon, p.lat, VECTOR_ZOOMS), vectorTpl);
  const b = tileUrls(corridorTiles(p.lon, p.lat, DEM_ZOOMS), DEM_TEMPLATE);
  return [
    ...a.map((u, i) => ({ u, r: (i + 0.5) / a.length })),
    ...b.map((u, i) => ({ u, r: (i + 0.5) / b.length })),
  ]
    .sort((x, y) => x.r - y.r)
    .slice(0, max)
    .map((e) => e.u);
}

const ZXY = /\/\d+\/\d+\/\d+(\.[a-z0-9]+)?$/i;

/**
 * OpenFreeMap tiles, sprites and glyphs (but not the style, nor the TileJSON at
 * /planet: both name the current tile version and must stay fresh) and
 * Terrarium DEM tiles.
 */
export function isTileRequest(url: string): boolean {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return false;
  }
  if (u.protocol !== 'https:') return false;
  if (u.origin === OPENFREEMAP) return !u.pathname.startsWith('/styles/') && u.pathname !== '/planet' && u.pathname !== '/planet/';
  if (u.origin === 'https://s3.amazonaws.com') return u.pathname.startsWith('/elevation-tiles-prod/terrarium/') && ZXY.test(u.pathname);
  return false;
}

export function cacheKey(url: string): string {
  const u = new URL(url);
  return u.origin + u.pathname;
}

/** Entries to delete (oldest first) so `adding` more fit under `max`; deletes in steps of 10 % of max. */
export function evictCount(entries: number, adding: number, max = MAX_ENTRIES): number {
  const over = entries + adding - max;
  if (over <= 0) return 0;
  const step = Math.max(1, Math.floor(max / 10));
  return Math.ceil(over / step) * step;
}

export async function vectorTemplate(fetchImpl: typeof fetch = fetch): Promise<string> {
  const res = await fetchImpl(`${OPENFREEMAP}/planet`);
  if (!res.ok) throw new Error(`tilejson ${res.status}`);
  const j = (await res.json()) as { tiles?: unknown };
  const t = Array.isArray(j.tiles) ? j.tiles[0] : undefined;
  if (typeof t !== 'string') throw new Error('tilejson has no tiles');
  return t;
}
