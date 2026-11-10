// Zone cut-offs. Mirror internal/stats/stats.go (PowerBounds, HRBounds): a value is in
// zone i when it is below the i-th upper edge and not below the previous one.
export const POWER_EDGES = [0.55, 0.75, 0.9, 1.05, 1.2, 1.5] as const;
export const HR_EDGES = [0.6, 0.7, 0.8, 0.9] as const;

export const POWER_ZONES = [
  { name: 'Active recovery', blurb: 'Very easy spinning; recovery between hard days.' },
  { name: 'Endurance', blurb: 'All-day aerobic pace; builds the base.' },
  { name: 'Tempo', blurb: 'Steady and brisk; comfortably hard.' },
  { name: 'Threshold', blurb: 'Around FTP; sustainable for about an hour.' },
  { name: 'VO2 max', blurb: 'Hard intervals of 3 to 8 minutes.' },
  { name: 'Anaerobic', blurb: 'Short, very hard efforts of 30 s to 3 min.' },
  { name: 'Neuromuscular', blurb: 'All-out sprints of a few seconds.' },
] as const;

export const HR_ZONES = [
  { name: 'Recovery', blurb: 'Easy effort, below 60% of max heart rate.' },
  { name: 'Endurance', blurb: 'Aerobic base; conversation pace.' },
  { name: 'Tempo', blurb: 'Brisk, controlled breathing.' },
  { name: 'Threshold', blurb: 'Hard; near the lactate threshold.' },
  { name: 'Maximum', blurb: 'Above 90% of max heart rate.' },
] as const;

export type ZoneKind = 'power' | 'hr';

export interface ZoneRange {
  /** 1-based zone number. */
  z: number;
  name: string;
  blurb: string;
  /** Inclusive integer range in watts or bpm; lo is null for zone 1, hi for the top zone. */
  lo: number | null;
  hi: number | null;
  /** Percent of FTP or max HR, same open-ended convention. */
  pctLo: number | null;
  pctHi: number | null;
}

/** Upper edges of zones 1..n-1 in watts or bpm. */
export function zoneBounds(kind: ZoneKind, base: number): number[] {
  return (kind === 'power' ? POWER_EDGES : HR_EDGES).map((e) => base * e);
}

/** One entry per zone. Without a positive base only names and descriptions are filled. */
export function zoneRanges(kind: ZoneKind, base?: number | null): ZoneRange[] {
  const zones = kind === 'power' ? POWER_ZONES : HR_ZONES;
  const edges = kind === 'power' ? POWER_EDGES : HR_EDGES;
  const ok = !!base && base > 0;
  return zones.map((zone, i) => ({
    z: i + 1,
    ...zone,
    lo: ok && i > 0 ? Math.round(base * edges[i - 1]) : null,
    hi: ok && i < edges.length ? Math.round(base * edges[i]) - 1 : null,
    pctLo: i > 0 ? Math.round(edges[i - 1] * 100) + 1 : null,
    pctHi: i < edges.length ? Math.round(edges[i] * 100) : null,
  }));
}

/** "155–206 W", "<154 W", "376+ W". Empty when the range is unknown. */
export function rangeLabel(r: ZoneRange, unit: string): string {
  if (r.lo === null && r.hi === null) return '';
  if (r.lo === null) return `<${r.hi! + 1} ${unit}`;
  if (r.hi === null) return `${r.lo}+ ${unit}`;
  return `${r.lo}–${r.hi} ${unit}`;
}

/** "56–75% FTP", "<55% FTP", ">150% FTP". */
export function pctLabel(r: ZoneRange, of: string): string {
  if (r.pctLo === null) return `<${r.pctHi}% ${of}`;
  if (r.pctHi === null) return `>${r.pctLo - 1}% ${of}`;
  return `${r.pctLo}–${r.pctHi}% ${of}`;
}
