import { useEffect, useMemo, useRef } from 'react';
import { profileGeometry } from '@/lib/profile-geometry';
import type { Profile } from '@/lib/route-math';
import type { TickStore } from '@/lib/tick-store';
import { prepCanvas, tok, useCanvasBox } from './canvas';

type Series = { distance: number[]; ele: number[]; grade: number[] };

function fromProfile(p: Profile): Series {
  const n = p.ele_m.length;
  const distance = new Array<number>(n);
  for (let i = 0; i < n; i++) distance[i] = Math.min(i * p.step_m, p.length_m);
  return { distance, ele: p.ele_m, grade: p.grade_pct };
}

function fromSynthetic(s: { distance: number[]; ele: number[] }): Series {
  const grade = s.ele.map((e, i) => {
    if (i === 0) return 0;
    const dd = s.distance[i] - s.distance[i - 1];
    return dd > 0 ? ((e - s.ele[i - 1]) / dd) * 100 : 0;
  });
  if (grade.length > 1) grade[0] = grade[1];
  return { distance: s.distance, ele: s.ele, grade };
}

const PAD = 12;

/** Grade bands: descent blue, easy green, steady amber, hard red. */
export function gradeColor(g: number): string {
  if (g < 0) return tok('--accent', '#54a9ff');
  if (g <= 2) return tok('--ok', '#6fd36a');
  if (g <= 6) return tok('--warn', '#f1c232');
  return tok('--danger', '#ff6b63');
}

/**
 * Elevation profile on a canvas, filled by grade. With a TickStore the rider's
 * position is a line moved straight in the DOM from a requestAnimationFrame.
 * Moving a pointer over the profile reports the distance under it.
 */
export function ProfileCanvas(p: {
  profile: Profile | null;
  synthetic?: { distance: number[]; ele: number[] };
  cursor?: TickStore;
  height?: number;
  onScrub?(distanceM: number | null): void;
}) {
  const { box, canvas, width } = useCanvasBox();
  const h = p.height ?? 180;
  const series = useMemo<Series | null>(
    () => (p.profile ? fromProfile(p.profile) : p.synthetic ? fromSynthetic(p.synthetic) : null),
    [p.profile, p.synthetic],
  );
  const geo = useMemo(() => (series && width > 0 ? profileGeometry(series, width, h, PAD) : null), [series, width, h]);
  const length = series && series.distance.length ? series.distance[series.distance.length - 1] : 0;

  useEffect(() => {
    const c = canvas.current;
    if (!c || !series || !geo || width <= 0) return;
    const ctx = prepCanvas(c, width, h);
    if (!ctx) return;
    const n = series.distance.length;
    const base = h - PAD;
    const stride = Math.max(1, Math.floor(n / Math.max(1, width)));
    ctx.globalAlpha = 0.9;
    for (let i = 0; i < n - 1; i += stride) {
      const j = Math.min(i + stride, n - 1);
      let g = 0;
      for (let k = i + 1; k <= j; k++) g += series.grade[k];
      ctx.fillStyle = gradeColor(g / (j - i));
      ctx.beginPath();
      ctx.moveTo(geo.x(series.distance[i]), base);
      ctx.lineTo(geo.x(series.distance[i]), geo.y(series.ele[i]));
      ctx.lineTo(geo.x(series.distance[j]), geo.y(series.ele[j]));
      ctx.lineTo(geo.x(series.distance[j]), base);
      ctx.closePath();
      ctx.fill();
    }
    ctx.globalAlpha = 1;
    ctx.strokeStyle = tok('--chalk', '#eaf1ee');
    ctx.lineWidth = 1.5;
    ctx.lineJoin = 'round';
    ctx.beginPath();
    for (let i = 0; i < n; i += stride) {
      const x = geo.x(series.distance[i]);
      const y = geo.y(series.ele[i]);
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.lineTo(geo.x(series.distance[n - 1]), geo.y(series.ele[n - 1]));
    ctx.stroke();
    ctx.fillStyle = tok('--dim', '#93a8ae');
    ctx.font = '12px "Saira Variable", system-ui, sans-serif';
    ctx.textBaseline = 'top';
    ctx.fillText(`${Math.round(geo.maxEle)} m`, PAD + 2, 2);
    ctx.textBaseline = 'bottom';
    ctx.textAlign = 'right';
    ctx.fillText(`${(length / 1000).toFixed(1)} km`, width - PAD, h - 1);
    ctx.textAlign = 'left';
    ctx.fillText(`${Math.round(geo.minEle)} m`, PAD + 2, h - 1);
  }, [series, geo, width, h, canvas, length]);

  // Ride cursor: no React state, one DOM write per frame.
  const cur = useRef<HTMLDivElement>(null);
  const geoRef = useRef(geo);
  geoRef.current = geo;
  useEffect(() => {
    const store = p.cursor;
    if (!store) return;
    let raf = 0;
    const draw = () => {
      raf = 0;
      const el = cur.current;
      const g = geoRef.current;
      const t = store.latest;
      if (!el || !g || !t) return;
      el.style.transform = `translateX(${g.x(t.distance_m)}px)`;
      el.dataset.distanceM = String(Math.round(t.distance_m));
      el.hidden = false;
    };
    draw();
    const unsub = store.subscribe(() => {
      if (!raf) raf = requestAnimationFrame(draw);
    });
    return () => {
      unsub();
      if (raf) cancelAnimationFrame(raf);
    };
  }, [p.cursor, geo]);

  function scrub(e: React.PointerEvent<HTMLDivElement>) {
    if (!p.onScrub || !geo || length <= 0) return;
    const r = e.currentTarget.getBoundingClientRect();
    const f = (e.clientX - r.left - PAD) / Math.max(1, width - 2 * PAD);
    p.onScrub(Math.max(0, Math.min(1, f)) * length);
  }

  return (
    <div
      ref={box}
      className="relative w-full touch-pan-y overflow-hidden rounded-control bg-ground"
      style={{ height: h }}
      onPointerMove={scrub}
      onPointerDown={scrub}
      onPointerLeave={() => p.onScrub?.(null)}
    >
      <canvas
        ref={canvas}
        role="img"
        aria-label={
          series
            ? `Elevation profile, ${(length / 1000).toFixed(1)} km, from ${Math.round(geo?.minEle ?? 0)} to ${Math.round(geo?.maxEle ?? 0)} m`
            : 'Elevation profile'
        }
        className="block"
        style={{ width, height: h }}
      />
      {p.cursor && (
        <div
          ref={cur}
          hidden
          data-testid="route-pos"
          data-distance-m="0"
          className="pointer-events-none absolute inset-y-0 left-0 w-0.5 bg-chalk shadow-[0_0_8px_2px_var(--accent)]"
        />
      )}
    </div>
  );
}
