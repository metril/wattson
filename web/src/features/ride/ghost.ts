/** A ghost track point: [distance_m, elapsed_s]. */
export type GhostPoint = readonly [number, number];

/**
 * Seconds the rider is behind (positive) or ahead of (negative) the ghost at
 * distance d and elapsed time t. A moving cursor makes consecutive calls
 * amortized O(1); it only walks back when the distance does (a restart).
 * Returns null before the first point or past the last one.
 */
export class GhostCursor {
  private i = 0;
  constructor(private readonly pts: readonly GhostPoint[]) {}

  delta(d: number, t: number): number | null {
    const p = this.pts;
    const n = p.length;
    if (n === 0 || d < p[0][0] || d > p[n - 1][0]) return null;
    let i = this.i;
    while (i > 0 && p[i][0] > d) i--;
    while (i < n - 1 && p[i + 1][0] <= d) i++;
    this.i = i;
    if (i === n - 1) return t - p[i][1];
    const [d0, t0] = p[i];
    const [d1, t1] = p[i + 1];
    const ghostT = d1 > d0 ? t0 + ((d - d0) / (d1 - d0)) * (t1 - t0) : t0;
    return t - ghostT;
  }
}

/** "+3 s" behind, "-3 s" ahead; whole seconds, then m:ss from a minute. */
export function fmtDelta(s: number): string {
  const a = Math.round(Math.abs(s));
  const sign = s > 0.5 ? '+' : s < -0.5 ? '-' : '';
  return `${sign}${a < 60 ? `${a} s` : `${Math.floor(a / 60)}:${String(a % 60).padStart(2, '0')}`}`;
}
