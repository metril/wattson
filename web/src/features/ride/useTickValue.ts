import { useEffect, useState } from 'react';
import type { TickStore } from '@/lib/tick-store';
import type { Tick } from '@/lib/ws-types';

/**
 * A value derived from the latest tick, as React state that changes only when
 * the value does (a mode, a workout id), so ticks do not re-render the caller.
 */
export function useTickValue<T extends string | number>(ticks: TickStore, pick: (t: Tick) => T, empty: T): T {
  const [v, setV] = useState<T>(() => (ticks.latest ? pick(ticks.latest) : empty));
  useEffect(() => {
    const apply = (t: Tick) => setV(pick(t)); // setState bails out on an equal primitive
    if (ticks.latest) apply(ticks.latest);
    return ticks.subscribe(apply);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ticks]);
  return v;
}
