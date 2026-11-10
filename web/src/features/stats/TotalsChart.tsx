import { useEffect, useState } from 'react';
import { prepCanvas, tok, useCanvasBox } from '@/features/routes/canvas';

const PAD = 14;
const H = 180;

export interface Bar {
  label: string;
  value: number;
}

/** One bar per period. `fmt` formats a value for the readout and the axis maximum. */
export function TotalsChart({ title, bars, fmt }: { title: string; bars: Bar[]; fmt: (v: number) => string }) {
  const { box, canvas, width } = useCanvasBox();
  const [hover, setHover] = useState<number | null>(null);
  const top = Math.max(0, ...bars.map((b) => b.value));

  useEffect(() => {
    const c = canvas.current;
    if (!c || width <= 0 || bars.length === 0) return;
    const ctx = prepCanvas(c, width, H);
    if (!ctx) return;
    const slot = (width - 2 * PAD) / bars.length;
    const bw = Math.max(1, slot * 0.7);
    const base = H - PAD - 6;
    bars.forEach((b, i) => {
      const bh = top > 0 ? (b.value / top) * (base - PAD - 12) : 0;
      ctx.fillStyle = tok(i === hover ? '--chalk' : '--accent', '#54a9ff');
      ctx.fillRect(PAD + i * slot + (slot - bw) / 2, base - bh, bw, Math.max(bh, b.value > 0 ? 1 : 0));
    });
    ctx.fillStyle = tok('--dim', '#93a8ae');
    ctx.font = '12px "Saira Variable", system-ui, sans-serif';
    ctx.textBaseline = 'top';
    ctx.fillText(fmt(top), PAD + 2, 2);
    ctx.textBaseline = 'bottom';
    ctx.fillText(bars[0].label, PAD, H - 1);
    ctx.textAlign = 'right';
    ctx.fillText(bars[bars.length - 1].label, width - PAD, H - 1);
    ctx.textAlign = 'left';
  }, [bars, top, fmt, width, hover, canvas]);

  function point(e: React.PointerEvent<HTMLDivElement>) {
    if (bars.length === 0) return;
    const r = e.currentTarget.getBoundingClientRect();
    const f = (e.clientX - r.left - PAD) / Math.max(1, r.width - 2 * PAD);
    setHover(Math.max(0, Math.min(bars.length - 1, Math.floor(f * bars.length))));
  }

  const b = hover !== null ? bars[hover] : undefined;
  return (
    <section className="flex flex-col gap-1">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3">
        <h3 className="text-sm font-medium text-dim">{title}</h3>
        <p data-testid="totals-readout" className="numeral h-6 text-base text-chalk">
          {b ? `${b.label}: ${fmt(b.value)}` : ''}
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
        <canvas ref={canvas} role="img" aria-label={`${title} per period`} className="block" style={{ width, height: H }} />
      </div>
    </section>
  );
}
