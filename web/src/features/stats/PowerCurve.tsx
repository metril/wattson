import { useEffect, useMemo, useState } from 'react';
import { chartGeometry } from '@/lib/chart-geometry';
import { prepCanvas, tok, useCanvasBox } from '@/features/routes/canvas';

const PAD = 14;
const H = 220;
const TICKS = [1, 5, 15, 60, 300, 1200, 3600, 7200];

export interface CurveData {
  dur_s: number;
  watts: number;
  w_per_kg?: number;
}

/** 5 -> "5 s", 300 -> "5 min", 3600 -> "1 h". */
export function durLabel(s: number): string {
  if (s < 60) return `${s} s`;
  if (s < 3600) return `${Math.round(s / 60)} min`;
  return `${+(s / 3600).toFixed(1)} h`;
}

/**
 * Best power per duration on a log-scale time axis. `points` is the main
 * (accent) curve; `ride` optionally overlays a single ride's curve.
 */
export function PowerCurve(p: { points: CurveData[]; ride?: CurveData[]; rideLabel?: string; label?: string }) {
  const { box, canvas, width } = useCanvasBox();
  const [hover, setHover] = useState<number | null>(null);
  const label = p.label ?? 'Best power';
  const xs = useMemo(() => p.points.map((q) => Math.log10(q.dur_s)), [p.points]);
  const geo = useMemo(() => {
    const all = p.ride ? [...p.points, ...p.ride] : p.points;
    return chartGeometry(
      all.map((q) => Math.log10(q.dur_s)),
      all.map((q) => q.watts),
      width,
      H,
      PAD,
    );
  }, [p.points, p.ride, width]);

  useEffect(() => {
    const c = canvas.current;
    if (!c || width <= 0 || p.points.length === 0) return;
    const ctx = prepCanvas(c, width, H);
    if (!ctx) return;
    const accent = tok('--accent', '#54a9ff');
    const chalk = tok('--chalk', '#eaf1ee');
    ctx.font = '12px "Saira Variable", system-ui, sans-serif';
    ctx.fillStyle = tok('--dim', '#93a8ae');
    ctx.strokeStyle = tok('--line', '#2b3a40');
    ctx.lineWidth = 1;
    ctx.textBaseline = 'bottom';
    ctx.textAlign = 'center';
    const lo = Math.log10(p.points[0].dur_s);
    const hi = Math.log10(p.points[p.points.length - 1].dur_s);
    for (const t of TICKS) {
      const lt = Math.log10(t);
      if (lt < lo || lt > hi) continue;
      const x = geo.x(lt);
      ctx.beginPath();
      ctx.moveTo(x, PAD);
      ctx.lineTo(x, H - 16);
      ctx.stroke();
      ctx.fillText(durLabel(t), Math.min(Math.max(x, 22), width - 22), H - 1);
    }
    ctx.textAlign = 'left';
    ctx.textBaseline = 'top';
    ctx.fillText(`${Math.round(geo.max)} W`, PAD + 2, 2);
    const line = (pts: CurveData[], color: string, w: number, dash: number[]) => {
      ctx.beginPath();
      pts.forEach((q, i) => {
        const x = geo.x(Math.log10(q.dur_s));
        const y = geo.y(q.watts) - 8;
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
      });
      ctx.setLineDash(dash);
      ctx.strokeStyle = color;
      ctx.lineWidth = w;
      ctx.lineJoin = 'round';
      ctx.stroke();
      ctx.setLineDash([]);
    };
    line(p.points, accent, 2.5, []);
    if (p.ride && p.ride.length > 0) line(p.ride, chalk, 2, [5, 4]);
    if (hover !== null && hover < p.points.length) {
      const q = p.points[hover];
      ctx.beginPath();
      ctx.arc(geo.x(Math.log10(q.dur_s)), geo.y(q.watts) - 8, 4, 0, Math.PI * 2);
      ctx.fillStyle = accent;
      ctx.fill();
    }
  }, [p.points, p.ride, geo, width, hover, canvas]);

  function point(e: React.PointerEvent<HTMLDivElement>) {
    if (xs.length === 0) return;
    const r = e.currentTarget.getBoundingClientRect();
    const f = Math.max(0, Math.min(1, (e.clientX - r.left - PAD) / Math.max(1, r.width - 2 * PAD)));
    const target = xs[0] + f * (xs[xs.length - 1] - xs[0]);
    let best = 0;
    for (let i = 1; i < xs.length; i++) if (Math.abs(xs[i] - target) < Math.abs(xs[best] - target)) best = i;
    setHover(best);
  }

  const q = hover !== null ? p.points[hover] : undefined;
  const mine = q && p.ride ? p.ride.find((r) => r.dur_s === q.dur_s) : undefined;
  const readout = q
    ? `${durLabel(q.dur_s)}: ${Math.round(q.watts)} W${q.w_per_kg ? ` (${q.w_per_kg.toFixed(2)} W/kg)` : ''}${
        mine ? ` · ${p.rideLabel ?? 'ride'} ${Math.round(mine.watts)} W` : ''
      }`
    : '';
  const first = p.points[0]?.dur_s ?? 0;
  const last = p.points[p.points.length - 1]?.dur_s ?? 0;

  return (
    <section className="flex flex-col gap-1">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3">
        <h3 className="text-sm font-medium text-dim">{label}</h3>
        <p data-testid="curve-readout" className="numeral h-6 text-base text-chalk">
          {readout}
        </p>
      </div>
      <div
        ref={box}
        className="relative w-full touch-pan-y overflow-hidden rounded-control bg-ground"
        style={{ height: H }}
        onPointerMove={point}
        onPointerDown={point}
        onPointerLeave={() => setHover(null)}
      >
        <canvas
          ref={canvas}
          role="img"
          aria-label={`${label} from ${durLabel(first)} to ${durLabel(last)}`}
          className="block"
          style={{ width, height: H }}
        />
      </div>
      {p.ride && <p className="text-xs text-dim">Solid: your best. Dashed: {p.rideLabel ?? 'this ride'}.</p>}
    </section>
  );
}
