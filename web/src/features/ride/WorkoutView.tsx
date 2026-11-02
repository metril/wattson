import { useEffect, useMemo, useRef } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Banner } from '@/components/Banner';
import { api, unwrap } from '@/lib/api';
import { useTickText, type TickStore } from '@/lib/tick-store';
import { mmss, resolve, totalS, type Block, type RiderLite, type WorkoutType } from '@/lib/workout-math';
import { CHART_W, WorkoutChart } from '@/features/workouts/WorkoutChart';
import { DEFAULT_RIDER, riderLite, workoutKey } from '@/features/workouts/queries';
import { RIDERS_KEY } from '@/pages/Riders';
import { getSelectedRiderId } from '@/lib/rider';
import type { Tick } from '@/lib/ws-types';

const signed = (n: number) => (n > 0 ? `+${n}` : String(n));

/** A block's target as shown to the rider: "L9", "+2 → L7", "250 W" or "75 % FTP · 150 W". */
export function describeTarget(type: WorkoutType, b: Block, value: number): string {
  if (type === 'interval') return b.level !== undefined ? `L${value}` : `${signed(b.level_offset ?? 0)} → L${value}`;
  return b.ftp_pct !== undefined ? `${b.ftp_pct} % FTP · ${value} W` : `${value} W`;
}

/**
 * The workout being ridden: block position, target, next block, a countdown
 * and the chart with a cursor. The text and the cursor follow the TickStore
 * from requestAnimationFrame, so ticks never re-render this component.
 */
export function WorkoutView({ workoutId, ticks, riderId }: { workoutId: number; ticks: TickStore; riderId?: number }) {
  const workout = useQuery({
    queryKey: workoutKey(workoutId),
    staleTime: Infinity,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/workouts/{id}', { params: { path: { id: workoutId } } })),
  });
  const riders = useQuery({
    queryKey: RIDERS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/riders')),
  });
  const list = riders.data ?? [];
  const picked = [riderId, getSelectedRiderId()].map((id) => list.find((r) => r.id === id)).find(Boolean) ?? list[0];
  const rider: RiderLite = useMemo(() => (picked ? riderLite(picked) : DEFAULT_RIDER), [picked]);

  if (workout.isError) {
    return <Banner tone="warn">This workout is no longer available. Live metrics continue without it.</Banner>;
  }
  if (!workout.data) return null;
  return <Body type={workout.data.type} blocks={workout.data.blocks} rider={rider} ticks={ticks} />;
}

function Body({ type, blocks, rider, ticks }: { type: WorkoutType; blocks: Block[]; rider: RiderLite; ticks: TickStore }) {
  const bars = useMemo(() => resolve(type, blocks, rider), [type, blocks, rider]);
  const total = totalS(blocks);
  const at = (i: number) => (blocks[i] ? describeTarget(type, blocks[i], bars[i].value) : '–');

  const pos = useTickText(ticks, (t) => `${t.block_index + 1}/${t.block_count}`);
  const label = useTickText(ticks, (t) => blocks[t.block_index]?.label ?? '');
  const target = useTickText(ticks, (t) => at(t.block_index));
  const cadence = useTickText(ticks, (t) => (blocks[t.block_index]?.cadence ? `Cadence ${blocks[t.block_index].cadence}` : ''));
  const next = useTickText(ticks, (t) => (t.block_index + 1 < blocks.length ? `Next: ${at(t.block_index + 1)}` : 'Last block'));
  const left = useTickText(ticks, (t) => mmss(t.block_remaining_s));

  // The chart cursor moves by writing its x attributes, not by re-rendering.
  const chart = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let raf = 0;
    const draw = () => {
      raf = 0;
      const t: Tick | null = ticks.latest;
      const line = chart.current?.querySelector('[data-testid=workout-cursor]');
      if (!t || !line || total <= 0) return;
      const i = Math.min(Math.max(t.block_index, 0), blocks.length - 1);
      const elapsed = (bars[i]?.startS ?? 0) + (bars[i]?.durS ?? 0) - t.block_remaining_s;
      const x = String((Math.min(Math.max(elapsed, 0), total) / total) * CHART_W);
      line.setAttribute('x1', x);
      line.setAttribute('x2', x);
    };
    draw();
    const unsub = ticks.subscribe(() => {
      if (!raf) raf = requestAnimationFrame(draw);
    });
    return () => {
      unsub();
      if (raf) cancelAnimationFrame(raf);
    };
  }, [ticks, bars, blocks.length, total]);

  return (
    <section aria-label="Workout" className="flex flex-col gap-3 rounded-panel border border-line bg-panel p-3 md:p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-1">
          <p className="flex items-baseline gap-2 text-sm font-medium text-dim">
            <span>Block</span>
            <span ref={pos} data-testid="block-pos" className="numeral text-chalk" />
            <span ref={label} data-testid="block-label" className="truncate" />
          </p>
          <span ref={target} data-testid="block-target" className="numeral text-numeral-md" />
          <span ref={cadence} data-testid="block-cadence" className="text-sm text-dim" />
          <span ref={next} data-testid="next-block" className="text-sm text-dim" />
        </div>
        <div className="flex flex-col items-end">
          <span className="text-sm font-medium text-dim">Left in block</span>
          <span ref={left} data-testid="block-countdown" className="numeral text-numeral-md" />
        </div>
      </div>
      <div ref={chart}>
        <WorkoutChart type={type} blocks={blocks} rider={rider} cursorS={0} />
      </div>
    </section>
  );
}
