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
