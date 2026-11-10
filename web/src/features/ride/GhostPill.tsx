import { useEffect, useMemo, useRef } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import type { TickStore } from '@/lib/tick-store';
import { ghostKey } from '@/features/routes/queries';
import { fmtDelta, GhostCursor, type GhostPoint } from './ghost';

/**
 * Seconds ahead of or behind the rider's personal record on this route. The
 * track is fetched once; ticks write the text straight to the DOM, so they
 * never re-render React. Renders nothing without a PR.
 */
export function GhostPill({ routeId, ticks }: { routeId: number; ticks: TickStore }) {
  const q = useQuery({
    queryKey: ghostKey(routeId),
    staleTime: Infinity,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/routes/{id}/ghost', { params: { path: { id: routeId } } })),
  });
  const cursor = useMemo(() => (q.data ? new GhostCursor(q.data.points as unknown as GhostPoint[]) : null), [q.data]);
  const el = useRef<HTMLDivElement>(null);
  const txt = useRef<HTMLSpanElement>(null);

  useEffect(() => {
    if (!cursor) return;
    let raf = 0;
    const write = () => {
      raf = 0;
      const t = ticks.latest;
      const d = t ? cursor.delta(t.distance_m, t.elapsed_s) : null;
      if (!el.current || !txt.current) return;
      el.current.dataset.state = d === null ? 'none' : d > 0.5 ? 'behind' : 'ahead';
      txt.current.textContent = d === null ? '–' : fmtDelta(d);
    };
    write();
    const unsub = ticks.subscribe(() => {
      if (!raf) raf = requestAnimationFrame(write);
    });
    return () => {
      unsub();
      if (raf) cancelAnimationFrame(raf);
    };
  }, [cursor, ticks]);

  if (!cursor) return null;
  return (
    <div
      ref={el}
      data-testid="ghost-pill"
      data-state="none"
      className="inline-flex items-baseline gap-2 self-start rounded-full border border-line bg-raised px-4 py-1 data-[state=ahead]:text-ok data-[state=behind]:text-warn"
    >
      <span className="text-sm text-dim">vs PR</span>
      <span ref={txt} className="numeral text-xl">
        –
      </span>
    </div>
  );
}
