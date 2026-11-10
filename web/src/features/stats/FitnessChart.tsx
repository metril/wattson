import { useEffect, useMemo, useState } from 'react';
import { chartGeometry } from '@/lib/chart-geometry';
import { prepCanvas, tok, useCanvasBox } from '@/features/routes/canvas';

const PAD = 14;
const H = 220;

export interface FitnessPoint {
  date: string;
  ctl: number;
  atl: number;
  tsb: number;
}

const SERIES = [
  { key: 'ctl', short: 'CTL', name: 'Fitness (CTL)', color: '--accent' },
  { key: 'atl', short: 'ATL', name: 'Fatigue (ATL)', color: '--danger' },
  { key: 'tsb', short: 'TSB', name: 'Form (TSB)', color: '--ok' },
] as const;

/** Daily CTL, ATL and TSB lines. Hovering reads out one day. */
export function FitnessChart({ days }: { days: FitnessPoint[] }) {
  const { box, canvas, width } = useCanvasBox();
  const [hover, setHover] = useState<number | null>(null);
  const geo = useMemo(() => {
    const ys: number[] = [];
    for (const d of days) ys.push(d.ctl, d.atl, d.tsb);
    return chartGeometry(
      days.map((_, i) => i),
      ys,
      width,
      H,
      PAD,
    );
  }, [days, width]);

  useEffect(() => {
    const c = canvas.current;
    if (!c || width <= 0 || days.length === 0) return;
    const ctx = prepCanvas(c, width, H);
    if (!ctx) return;
    ctx.strokeStyle = tok('--line', '#2b3a40');
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(PAD, geo.y(0));
    ctx.lineTo(width - PAD, geo.y(0));
    ctx.stroke();
    for (const s of SERIES) {
      ctx.beginPath();
      days.forEach((d, i) => {
        const x = geo.x(i);
        const y = geo.y(d[s.key]);
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
      });
      ctx.strokeStyle = tok(s.color, '#54a9ff');
      ctx.lineWidth = 2;
      ctx.lineJoin = 'round';
      ctx.stroke();
    }
    ctx.fillStyle = tok('--dim', '#93a8ae');
    ctx.font = '12px "Saira Variable", system-ui, sans-serif';
    ctx.textBaseline = 'top';
    ctx.fillText(`${Math.round(geo.max)}`, PAD + 2, 2);
    ctx.textBaseline = 'bottom';
    ctx.fillText(`${Math.round(geo.min)}`, PAD + 2, H - 1);
    ctx.textAlign = 'right';
    ctx.fillText(days[days.length - 1].date, width - PAD, H - 1);
    ctx.textAlign = 'left';
    if (hover !== null && hover < days.length) {
      const hx = geo.x(hover);
      ctx.strokeStyle = tok('--chalk', '#eaf1ee');
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(hx, PAD);
      ctx.lineTo(hx, H - PAD);
      ctx.stroke();
    }
  }, [days, geo, width, hover, canvas]);

  function point(e: React.PointerEvent<HTMLDivElement>) {
    if (days.length === 0) return;
    const r = e.currentTarget.getBoundingClientRect();
    const f = Math.max(0, Math.min(1, (e.clientX - r.left - PAD) / Math.max(1, r.width - 2 * PAD)));
    setHover(Math.round(f * (days.length - 1)));
  }

  const shown = days[hover ?? days.length - 1];
  return (
    <section className="flex flex-col gap-1">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3">
        <h3 className="text-sm font-medium text-dim">Fitness, fatigue and form</h3>
        {shown && (
          <p data-testid="fitness-readout" className="numeral text-base text-chalk">
            {shown.date} · {SERIES.map((s) => `${s.short} ${shown[s.key].toFixed(0)}`).join(' · ')}
          </p>
        )}
      </div>
      <div
        ref={box}
        className="relative w-full touch-pan-y overflow-hidden rounded-control bg-ground"
        style={{ height: H }}
        onPointerMove={point}
        onPointerDown={point}
        onPointerLeave={() => setHover(null)}
      >
        <canvas ref={canvas} role="img" aria-label="Fitness, fatigue and form over time" className="block" style={{ width, height: H }} />
      </div>
      <ul className="flex flex-wrap gap-x-4 text-xs text-dim">
        {SERIES.map((s) => (
          <li key={s.key} className="flex items-center gap-1.5">
            <span aria-hidden className="inline-block h-0.5 w-4" style={{ background: `var(${s.color})` }} />
            {s.name}
          </li>
        ))}
      </ul>
    </section>
  );
}
