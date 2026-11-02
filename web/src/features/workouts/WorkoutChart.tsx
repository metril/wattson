import { resolve, totalS, type Block, type RiderLite, type WorkoutType } from '@/lib/workout-math';

const W = 1000;
const H = 200;
// Power zone upper bounds as a fraction of FTP (Z1..Z6; above is Z7).
const ZONE_TOP = [0.55, 0.75, 0.9, 1.05, 1.2, 1.5];

function zoneFill(watts: number, ftp: number): string {
  if (ftp <= 0) return 'var(--accent)';
  const i = ZONE_TOP.findIndex((top) => watts / ftp <= top);
  return `var(--zone-${i === -1 ? 7 : i + 1})`;
}

/**
 * The workout as one bar per block: width is duration, height is the resolved
 * level (axis 1..rider max) or watts (axis 0..max(400, peak)). `cursorS` draws
 * a progress line.
 */
export function WorkoutChart(p: {
  type: WorkoutType;
  blocks: Block[];
  rider: RiderLite;
  cursorS?: number;
  /** Small decorative thumbnail for lists: hidden from assistive tech. */
  compact?: boolean;
  className?: string;
}) {
  const total = totalS(p.blocks);
  const bars = resolve(p.type, p.blocks, p.rider);
  const peak = Math.max(0, ...bars.map((b) => b.value));
  const axisMax = p.type === 'interval' ? Math.max(p.rider.max_level, peak, 1) : Math.max(400, peak);
  const unit = (v: number) => (p.type === 'interval' ? `Level ${v}` : `${v} W`);
  const cursorX = p.cursorS === undefined || total <= 0 ? null : (Math.min(Math.max(p.cursorS, 0), total) / total) * W;

  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      preserveAspectRatio="none"
      className={p.className ?? 'h-40 w-full'}
      {...(p.compact
        ? { 'aria-hidden': true }
        : { role: 'img', 'aria-label': `Workout preview, ${p.blocks.length} ${p.blocks.length === 1 ? 'block' : 'blocks'}` })}
    >
      <line x1={0} x2={W} y1={H} y2={H} stroke="var(--line)" strokeWidth={2} vectorEffect="non-scaling-stroke" />
      {total > 0 &&
        bars.map((b, i) => {
          const h = (b.value / axisMax) * H;
          return (
            <rect
              key={i}
              x={(b.startS / total) * W}
              y={H - h}
              width={(b.durS / total) * W}
              height={h}
              fill={p.type === 'erg' ? zoneFill(b.value, p.rider.ftp_w) : 'var(--accent)'}
              fillOpacity={0.85}
              stroke="var(--panel)"
              strokeWidth={1}
              vectorEffect="non-scaling-stroke"
            >
              <title>{unit(b.value)}</title>
            </rect>
          );
        })}
      {cursorX !== null && (
        <line
          data-testid="workout-cursor"
          x1={cursorX}
          x2={cursorX}
          y1={0}
          y2={H}
          stroke="var(--chalk)"
          strokeWidth={2}
          vectorEffect="non-scaling-stroke"
        />
      )}
    </svg>
  );
}
