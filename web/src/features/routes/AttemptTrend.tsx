import { useEffect, useMemo } from 'react';
import { chartGeometry } from '@/lib/chart-geometry';
import { hms } from '@/lib/format';
import { prepCanvas, tok, useCanvasBox } from './canvas';
import type { Attempt } from './AttemptsTable';

const PAD = 14;

/** Completed times over attempt order. Mount only with 2+ completed attempts (the box ref must exist on mount). Redraws only when the attempts or size change. */
export function AttemptTrend({ attempts, height = 140 }: { attempts: Attempt[]; height?: number }) {
  const { box, canvas, width } = useCanvasBox();
  const done = useMemo(() => attempts.filter((a) => a.completed), [attempts]);
  const xs = useMemo(() => done.map((_, i) => i), [done]);
  const ys = useMemo(() => done.map((a) => a.elapsed_s), [done]);
  const geo = useMemo(() => chartGeometry(xs, ys, width, height, PAD), [xs, ys, width, height]);

  useEffect(() => {
    const c = canvas.current;
    if (!c || width <= 0 || done.length < 2) return;
    const ctx = prepCanvas(c, width, height);
    if (!ctx) return;
    const color = tok('--accent', '#54a9ff');
    ctx.beginPath();
    done.forEach((_, i) => (i === 0 ? ctx.moveTo(geo.x(i), geo.y(ys[i])) : ctx.lineTo(geo.x(i), geo.y(ys[i]))));
    ctx.strokeStyle = color;
    ctx.lineWidth = 2;
    ctx.lineJoin = 'round';
    ctx.stroke();
    done.forEach((a, i) => {
      ctx.beginPath();
      ctx.arc(geo.x(i), geo.y(ys[i]), a.is_pr ? 5 : 3, 0, Math.PI * 2);
      ctx.fillStyle = a.is_pr ? tok('--ok', '#6fd36a') : color;
      ctx.fill();
    });
    ctx.fillStyle = tok('--dim', '#93a8ae');
    ctx.font = '12px "Saira Variable", system-ui, sans-serif';
    ctx.textBaseline = 'top';
    ctx.fillText(hms(geo.max), PAD + 2, 2);
    ctx.textBaseline = 'bottom';
    ctx.fillText(hms(geo.min), PAD + 2, height - 1);
  }, [done, ys, geo, width, height, canvas]);

  return (
    <div ref={box} className="relative w-full overflow-hidden rounded-control bg-ground" style={{ height }}>
      <canvas
        ref={canvas}
        role="img"
        aria-label={`Completed times, fastest ${hms(geo.min)}, slowest ${hms(geo.max)}`}
        className="block"
        style={{ width, height }}
      />
    </div>
  );
}
