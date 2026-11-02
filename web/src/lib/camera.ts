import { indexAt, positionAt, type Profile } from './route-math';

const R = 6371008.8;
const rad = (d: number) => (d * Math.PI) / 180;

function bearingBetween(a: [number, number], b: [number, number]): number {
  const p1 = rad(a[1]);
  const p2 = rad(b[1]);
  const dl = rad(b[0] - a[0]);
  const y = Math.sin(dl) * Math.cos(p2);
  const x = Math.cos(p1) * Math.sin(p2) - Math.sin(p1) * Math.cos(p2) * Math.cos(dl);
  return (((Math.atan2(y, x) * 180) / Math.PI) % 360 + 360) % 360;
}

function metres(a: [number, number], b: [number, number]): number {
  const dy = rad(b[1] - a[1]) * R;
  const dx = rad(b[0] - a[0]) * R * Math.cos(rad((a[1] + b[1]) / 2));
  return Math.hypot(dx, dy);
}

/**
 * Compass bearing in degrees [0, 360) from the position at d towards the one
 * aheadM further on (clamped to the route end). Where that span has no length
 * (duplicate points, the very end) the last segment with length decides.
 */
export function routeBearing(p: Profile, d: number, aheadM = 80): number {
  const from = positionAt(p, d);
  const to = positionAt(p, Math.min(d + aheadM, p.length_m));
  if (metres(from, to) >= 0.05) return bearingBetween(from, to);
  const n = p.lon.length;
  for (let i = Math.min(indexAt(p, d), n - 2); i >= 0; i--) {
    const a: [number, number] = [p.lon[i], p.lat[i]];
    const b: [number, number] = [p.lon[i + 1], p.lat[i + 1]];
    if (metres(a, b) >= 0.05) return bearingBetween(a, b);
  }
  return 0;
}

/** Turns prev towards target the short way: exponential (tauS) and at most maxRateDps. */
export function smoothBearing(prev: number, target: number, dtS: number, tauS = 1.5, maxRateDps = 45): number {
  if (!Number.isFinite(target) || !(dtS > 0)) return prev;
  const diff = ((((target - prev) % 360) + 540) % 360) - 180;
  let step = diff * (1 - Math.exp(-dtS / tauS));
  const cap = maxRateDps * dtS;
  step = Math.max(-cap, Math.min(cap, step));
  return (((prev + step) % 360) + 360) % 360;
}

/** Distance now: the last tick's plus speed times the time since, for at most 1.5 s, never past the end. */
export function extrapolate(lastD: number, speedKmh: number, sinceTickS: number, lengthM: number): number {
  const v = Math.max(0, speedKmh) / 3.6;
  const d = lastD + v * Math.min(Math.max(0, sinceTickS), 1.5);
  return Math.max(lastD, Math.min(lengthM, d));
}
