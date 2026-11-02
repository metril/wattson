/** Linear maps from data values to a padded canvas box. Flat or empty data stays finite (y centred). */
export function chartGeometry(
  xs: number[],
  ys: number[],
  w: number,
  h: number,
  pad: number,
): { x(v: number): number; y(v: number): number; min: number; max: number } {
  let xmin = Infinity;
  let xmax = -Infinity;
  for (const v of xs) {
    if (v < xmin) xmin = v;
    if (v > xmax) xmax = v;
  }
  let min = Infinity;
  let max = -Infinity;
  for (const v of ys) {
    if (v < min) min = v;
    if (v > max) max = v;
  }
  if (!Number.isFinite(xmin)) xmin = xmax = 0;
  if (!Number.isFinite(min)) min = max = 0;
  const xspan = xmax - xmin;
  const yspan = max - min;
  return {
    min,
    max,
    x: (v) => (xspan > 0 ? pad + ((v - xmin) / xspan) * (w - 2 * pad) : pad),
    y: (v) => (yspan > 0 ? h - pad - ((v - min) / yspan) * (h - 2 * pad) : h / 2),
  };
}
