import { useEffect, useMemo } from 'react';
import { previewLevels, type Profile, type RiderTune } from '@/lib/route-math';
import { prepCanvas, tok, useCanvasBox } from './canvas';

const H = 120;
const PAD_L = 34;
const PAD_Y = 10;

/** The resistance levels the bike would be told along the route, as a step chart from the rider's lowest to highest level. */
export function PreviewChart(p: { profile: Profile; rider: RiderTune }) {
  const { box, canvas, width } = useCanvasBox();
  const levels = useMemo(() => previewLevels(p.profile, p.rider), [p.profile, p.rider]);
  const stats = useMemo(() => {
    let lo = Infinity;
    let hi = -Infinity;
    for (const l of levels) {
      if (l < lo) lo = l;
      if (l > hi) hi = l;
    }
    return levels.length ? { min: lo, max: hi } : { min: 0, max: 0 };
  }, [levels]);

  useEffect(() => {
    const c = canvas.current;
    if (!c || width <= 0) return;
    const ctx = prepCanvas(c, width, H);
    if (!ctx) return;
    const lo = Math.max(1, p.rider.min_level);
    const hi = Math.max(lo + 1, p.rider.max_level);
    const x = (i: number) => PAD_L + (levels.length > 1 ? (i / (levels.length - 1)) * (width - PAD_L - 6) : 0);
    const y = (l: number) => H - PAD_Y - ((l - lo) / (hi - lo)) * (H - 2 * PAD_Y);
    const line = tok('--line', '#2a3d45');
    ctx.strokeStyle = line;
    ctx.lineWidth = 1;
    ctx.setLineDash([3, 4]);
    for (const l of [lo, p.rider.offset_level, hi]) {
      ctx.beginPath();
      ctx.moveTo(PAD_L, y(l));
      ctx.lineTo(width - 6, y(l));
      ctx.stroke();
    }
    ctx.setLineDash([]);
    ctx.fillStyle = tok('--dim', '#93a8ae');
    ctx.font = '12px "Saira Variable", system-ui, sans-serif';
    ctx.textAlign = 'right';
    ctx.textBaseline = 'middle';
    ctx.fillText(`L${hi}`, PAD_L - 6, y(hi) + 4);
    ctx.fillText(`L${lo}`, PAD_L - 6, y(lo) - 4);
    if (levels.length > 1) {
      const accent = tok('--accent', '#54a9ff');
      // Step path: hold each level until the next sample.
      ctx.beginPath();
      ctx.moveTo(x(0), y(levels[0]));
      const stride = Math.max(1, Math.floor(levels.length / Math.max(1, width)));
      let prev = levels[0];
      for (let i = stride; i < levels.length; i += stride) {
        ctx.lineTo(x(i), y(prev));
        ctx.lineTo(x(i), y(levels[i]));
        prev = levels[i];
      }
      ctx.lineTo(x(levels.length - 1), y(prev));
      ctx.strokeStyle = accent;
      ctx.lineWidth = 2;
      ctx.lineJoin = 'round';
      ctx.stroke();
      ctx.lineTo(x(levels.length - 1), y(lo));
      ctx.lineTo(x(0), y(lo));
      ctx.closePath();
      ctx.globalAlpha = 0.18;
      ctx.fillStyle = accent;
      ctx.fill();
      ctx.globalAlpha = 1;
    }
  }, [levels, width, p.rider, canvas]);

  return (
    <div>
      <div ref={box} className="w-full overflow-hidden rounded-control bg-ground" style={{ height: H }}>
        <canvas
          ref={canvas}
          role="img"
          aria-label={`Resistance preview, level ${stats.min} to ${stats.max}`}
          className="block"
          style={{ width, height: H }}
        />
      </div>
      <p className="numeral mt-2 flex gap-4 text-lg">
        <span>min L{stats.min}</span>
        <span>max L{stats.max}</span>
      </p>
    </div>
  );
}
