import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import { fmtSpeed, hms, rideDate, type Units } from '@/lib/format';
import { Card } from '@/components/Card';
import { AttemptsTable } from './AttemptsTable';
import { AttemptTrend } from './AttemptTrend';
import { routeStatsKey } from './queries';

/** The caller's attempts, personal record and time trend on one route. Hidden until there is a ride. */
export function AttemptsCard({ routeId, units }: { routeId: number; units: Units }) {
  const q = useQuery({
    queryKey: routeStatsKey(routeId),
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/routes/{id}/stats', { params: { path: { id: routeId } } })),
  });
  const s = q.data;
  if (!s || s.attempts === 0) return null;
  const items: [string, string][] = [
    ['Attempts', `${s.attempts} (${s.completed} completed)`],
    ['Best time', s.best_s != null ? hms(s.best_s) : '–'],
    ['Average time', s.avg_s != null ? hms(s.avg_s) : '–'],
    ['Last time', s.last_s != null ? hms(s.last_s) : '–'],
    ['Best speed', s.best_speed_kmh != null ? fmtSpeed(s.best_speed_kmh, units) : '–'],
    ['PR set', s.pr_date ? rideDate(s.pr_date) : '–'],
    ['Best NP', s.best_np_w != null ? `${s.best_np_w.toFixed(0)} W` : '–'],
    ['Avg power', s.avg_power_w != null ? `${s.avg_power_w.toFixed(0)} W` : '–'],
    ['Avg heart rate', s.avg_hr_bpm != null ? `${s.avg_hr_bpm.toFixed(0)} bpm` : '–'],
  ];
  return (
    <Card className="flex flex-col gap-4">
      <h2 className="numeral text-xl">Your rides</h2>
      <dl className="grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-3">
        {items.map(([label, value]) => (
          <div key={label}>
            <dt className="text-sm text-dim">{label}</dt>
            <dd className="numeral text-xl">{value}</dd>
          </div>
        ))}
      </dl>
      <AttemptTrend attempts={s.list} />
      <AttemptsTable attempts={s.list} units={units} />
    </Card>
  );
}
