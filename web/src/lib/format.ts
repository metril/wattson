const pad = (n: number) => String(n).padStart(2, '0');

/** Seconds as HH:MM:SS. Negative or non-finite input reads as zero. */
export function hms(seconds: number): string {
  const s = Number.isFinite(seconds) && seconds > 0 ? Math.floor(seconds) : 0;
  return `${pad(Math.floor(s / 3600))}:${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`;
}

/** Metres as kilometres with two decimals. */
export function km(m: number): string {
  return (m / 1000).toFixed(2);
}

/** Round to an integer string. */
export function fixed0(n: number): string {
  return n.toFixed(0);
}

/** Local date and time of an ISO timestamp, such as "2 Nov 2026, 05:05". */
export function rideDate(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '–' : d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

export type Units = 'metric' | 'imperial';

const M_PER_MI = 1609.344;
const M_PER_FT = 0.3048;
const KMH_PER_MPH = M_PER_MI / 1000;

/** Metres as km or miles, for charts. */
export const toDist = (m: number, u: Units): number => (u === 'imperial' ? m / M_PER_MI : m / 1000);
/** km/h as km/h or mph, for charts. */
export const toSpeed = (kmh: number, u: Units): number => (u === 'imperial' ? kmh / KMH_PER_MPH : kmh);
/** Metres as metres or feet, for charts. */
export const toElev = (m: number, u: Units): number => (u === 'imperial' ? m / M_PER_FT : m);

export const distUnit = (u: Units): string => (u === 'imperial' ? 'mi' : 'km');
export const speedUnit = (u: Units): string => (u === 'imperial' ? 'mph' : 'km/h');
export const elevUnit = (u: Units): string => (u === 'imperial' ? 'ft' : 'm');

/** Distance with unit, such as "2.76 km" or "1.71 mi". */
export const fmtDist = (m: number, u: Units): string => `${toDist(m, u).toFixed(2)} ${distUnit(u)}`;
/** Speed with unit, one decimal. */
export const fmtSpeed = (kmh: number, u: Units): string => `${toSpeed(kmh, u).toFixed(1)} ${speedUnit(u)}`;
/** Elevation with unit, whole numbers. */
export const fmtElev = (m: number, u: Units): string => `${toElev(m, u).toFixed(0)} ${elevUnit(u)}`;
