import type { QueryClient } from '@tanstack/react-query';
import { STATS_KEY } from '@/features/stats/queries';

/** Query key prefix of every ride query (list pages and details). */
export const RIDES_KEY = ['rides'] as const;

/** Rides changed (ended or deleted): refresh every route's attempts and PR ghost, and the stats pages. */
export function invalidateRideData(qc: QueryClient) {
  void qc.invalidateQueries({ queryKey: STATS_KEY });
  void qc.invalidateQueries({
    queryKey: ['routes'],
    predicate: (q) => q.queryKey[2] === 'stats' || q.queryKey[2] === 'ghost',
  });
}
