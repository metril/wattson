/** Linear maps from route distance and elevation to a padded canvas box. */
export function profileGeometry(
  p: { distance: number[]; ele: number[] },
  w: number,
  h: number,
  pad: number,
): { x(d: number): number; y(e: number): number; minEle: number; maxEle: number } {
  let minEle = Infinity;
  let maxEle = -Infinity;
  for (const e of p.ele) {
    if (e < minEle) minEle = e;
    if (e > maxEle) maxEle = e;
  }
  if (!Number.isFinite(minEle)) minEle = maxEle = 0;
  const len = p.distance.length ? p.distance[p.distance.length - 1] : 0;
  const span = maxEle - minEle;
  return {
    minEle,
    maxEle,
    x: (d) => (len > 0 ? pad + (d / len) * (w - 2 * pad) : pad),
    y: (e) => (span > 0 ? h - pad - ((e - minEle) / span) * (h - 2 * pad) : h / 2),
  };
}
