import { useEffect, useMemo, useState } from 'react';
import { chartGeometry } from '@/lib/chart-geometry';
import { prepCanvas, tok, useCanvasBox } from '@/features/routes/canvas';

const PAD = 12;

/** Index of the x closest to v in an ascending array. */
function nearest(xs: number[], v: number): number {
  let lo = 0;
  let hi = xs.length - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (xs[mid] < v) lo = mid + 1;
    else hi = mid;
  }
  return lo > 0 && Math.abs(xs[lo - 1] - v) <= Math.abs(xs[lo] - v) ? lo - 1 : lo;
}

/** One ride series over distance (km) on a canvas. Moving a pointer over it reads out the nearest sample. */
export function SeriesChart(p: {
  x: number[];
  y: number[];
  label: string;
  unit: string;
  color: string;
  height?: number;
}) {
  const { box, canvas, width } = useCanvasBox();
  const h = p.height ?? 140;
  const [hover, setHover] = useState<number | null>(null);
  const geo = useMemo(() => chartGeometry(p.x, p.y, width, h, PAD), [p.x, p.y, width, h]);
  const decimals = p.unit === 'km/h' ? 1 : 0;

  useEffect(() => {
    const c = canvas.current;
    const n = Math.min(p.x.length, p.y.length);
    if (!c || width <= 0 || n === 0) return;
    const ctx = prepCanvas(c, width, h);
    if (!ctx) return;
    const color = p.color.startsWith('--') ? tok(p.color, '#54a9ff') : p.color;
    ctx.beginPath();
    ctx.moveTo(geo.x(p.x[0]), h - PAD);
    for (let i = 0; i < n; i++) ctx.lineTo(geo.x(p.x[i]), geo.y(p.y[i]));
    ctx.lineTo(geo.x(p.x[n - 1]), h - PAD);
    ctx.closePath();
    ctx.globalAlpha = 0.18;
    ctx.fillStyle = color;
    ctx.fill();
    ctx.globalAlpha = 1;
    ctx.beginPath();
    for (let i = 0; i < n; i++) {
      const px = geo.x(p.x[i]);
      const py = geo.y(p.y[i]);
      if (i === 0) ctx.moveTo(px, py);
      else ctx.lineTo(px, py);
    }
    ctx.strokeStyle = color;
    ctx.lineWidth = 2;
    ctx.lineJoin = 'round';
    ctx.stroke();
    ctx.fillStyle = tok('--dim', '#93a8ae');
    ctx.font = '12px "Saira Variable", system-ui, sans-serif';
    ctx.textBaseline = 'top';
    ctx.fillText(`${geo.max.toFixed(decimals)} ${p.unit}`, PAD + 2, 2);
    ctx.textBaseline = 'bottom';
    ctx.fillText(`${geo.min.toFixed(decimals)} ${p.unit}`, PAD + 2, h - 1);
    ctx.textAlign = 'right';
    ctx.fillText(`${p.x[n - 1].toFixed(1)} km`, width - PAD, h - 1);
    ctx.textAlign = 'left';
    if (hover !== null && hover < n) {
      const hx = geo.x(p.x[hover]);
      ctx.strokeStyle = tok('--chalk', '#eaf1ee');
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(hx, PAD);
      ctx.lineTo(hx, h - PAD);
      ctx.stroke();
      ctx.beginPath();
      ctx.arc(hx, geo.y(p.y[hover]), 4, 0, Math.PI * 2);
      ctx.fillStyle = color;
      ctx.fill();
    }
  }, [p.x, p.y, p.color, p.unit, geo, width, h, canvas, hover, decimals]);

  function point(e: React.PointerEvent<HTMLDivElement>) {
    const n = Math.min(p.x.length, p.y.length);
    if (n === 0) return;
    const r = e.currentTarget.getBoundingClientRect();
    const f = Math.max(0, Math.min(1, (e.clientX - r.left - PAD) / Math.max(1, r.width - 2 * PAD)));
    setHover(nearest(p.x, p.x[0] + f * (p.x[n - 1] - p.x[0])));
  }

  const readout = hover !== null && hover < p.y.length ? `${p.y[hover].toFixed(decimals)} ${p.unit} @ ${p.x[hover].toFixed(2)} km` : '';

  return (
    <section className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-3">
        <h3 className="text-sm font-medium text-dim">{p.label}</h3>
        <p data-testid="chart-readout" className="numeral h-6 text-base text-chalk">
          {readout}
        </p>
      </div>
      <div
        ref={box}
        className="relative w-full touch-pan-y overflow-hidden rounded-control bg-ground"
        style={{ height: h }}
        onPointerMove={point}
        onPointerDown={point}
        onPointerLeave={() => setHover(null)}
      >
        <canvas
          ref={canvas}
          role="img"
          aria-label={`${p.label} over distance, ${geo.min.toFixed(decimals)} to ${geo.max.toFixed(decimals)} ${p.unit}`}
          className="block"
          style={{ width, height: h }}
        />
      </div>
    </section>
  );
}
