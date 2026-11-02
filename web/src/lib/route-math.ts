import type { components } from './openapi';

export type Profile = components['schemas']['ProfileOut'];
export type RiderTune = {
  gain: number;
  offset_level: number;
  downhill_scale: number;
  min_level: number;
  max_level: number;
};

/** Distance of sample i in metres; the last sample is capped at the route length. */
export function distAt(p: Profile, i: number): number {
  return Math.min(i * p.step_m, p.length_m);
}

/** Index of the last sample at or before distance d (clamped to the profile). */
export function indexAt(p: Profile, d: number): number {
  const n = p.grade_pct.length;
  if (n === 0) return 0;
  let lo = 0;
  let hi = n - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (distAt(p, mid) <= d) lo = mid;
    else hi = mid - 1;
  }
  return lo;
}

function lerpAt(p: Profile, d: number, a: number[]): number {
  const n = a.length;
  if (n === 0) return 0;
  const i = indexAt(p, d);
  if (i >= n - 1 || d <= distAt(p, i)) return a[i];
  const d0 = distAt(p, i);
  const t = (d - d0) / (distAt(p, i + 1) - d0);
  return a[i] + (a[i + 1] - a[i]) * t;
}

/** [lon, lat] at distance d, linearly interpolated. */
export function positionAt(p: Profile, d: number): [number, number] {
  return [lerpAt(p, d, p.lon), lerpAt(p, d, p.lat)];
}

/** Grade in percent at distance d, linearly interpolated. */
export function gradeAt(p: Profile, d: number): number {
  return lerpAt(p, d, p.grade_pct);
}

/** Resistance level for a grade; the same formula as engine.Mapper.Target (never below 1). */
export function levelFor(gradePct: number, r: RiderTune, bias = 0): number {
  const lo = Math.max(1, r.min_level);
  const hi = Math.max(lo, r.max_level);
  const g = gradePct < 0 ? gradePct * r.downhill_scale : gradePct;
  const v = r.offset_level + bias + g * r.gain;
  if (Number.isNaN(v)) return lo;
  return Math.max(lo, Math.min(hi, v));
}

/** Rounded level at every sample, as the bike would be told. */
export function previewLevels(p: Profile, r: RiderTune): Int16Array {
  const out = new Int16Array(p.grade_pct.length);
  for (let i = 0; i < out.length; i++) out[i] = Math.round(levelFor(p.grade_pct[i], r));
  return out;
}

/** 10 m points for constant-grade segments, starting at 100 m elevation. */
export function syntheticProfile(segs: { length_m: number; grade_pct: number }[]): {
  distance: number[];
  ele: number[];
} {
  const distance = [0];
  const ele = [100];
  let d = 0;
  let e = 100;
  for (const s of segs) {
    if (!(s.length_m > 0) || !Number.isFinite(s.grade_pct)) continue;
    const end = d + s.length_m;
    while (d < end - 1e-9) {
      const step = Math.min(10, end - d);
      d += step;
      e += (step * s.grade_pct) / 100;
      distance.push(Math.round(d * 1e6) / 1e6);
      ele.push(e);
    }
  }
  return { distance, ele };
}
