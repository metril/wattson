import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { useUnits } from '@/lib/auth';
import { fmtDist, fmtElev, hms, type Units } from '@/lib/format';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Card } from '@/components/Card';
import { Select } from '@/components/Select';
import { FitnessChart } from '@/features/stats/FitnessChart';
import { PowerCurve } from '@/features/stats/PowerCurve';
import { TotalsChart } from '@/features/stats/TotalsChart';
import { curveKey, STATS_KEY } from '@/features/stats/queries';
import { RIDERS_KEY } from './Riders';

type Rider = components['schemas']['RiderOut'];
type Period = 'week' | 'month' | 'year';
type Window = '90d' | '1y' | 'all';
type Metric = 'distance_m' | 'duration_s' | 'ascent_m' | 'kj' | 'tss' | 'rides';

const tz = (() => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
})();

const METRICS: { key: Metric; label: string; fmt: (v: number, u: Units) => string }[] = [
  { key: 'distance_m', label: 'Distance', fmt: (v, u) => fmtDist(v, u) },
  { key: 'duration_s', label: 'Time', fmt: (v) => hms(v) },
  { key: 'ascent_m', label: 'Climbing', fmt: (v, u) => fmtElev(v, u) },
  { key: 'kj', label: 'Work', fmt: (v) => `${Math.round(v)} kJ` },
  { key: 'tss', label: 'Training stress', fmt: (v) => `${Math.round(v)} TSS` },
  { key: 'rides', label: 'Rides', fmt: (v) => String(Math.round(v)) },
];

const barLabel = (start: string, p: Period) => (p === 'year' ? start.slice(0, 4) : p === 'month' ? start.slice(0, 7) : start);

function Tile({ label, children }: { label: string; children: string }) {
  return (
    <div className="flex flex-col gap-0.5 rounded-control bg-raised px-3 py-2">
      <span className="text-xs text-dim">{label}</span>
      <span className="numeral text-xl">{children}</span>
    </div>
  );
}

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? '' : 's'}`;

export function StatsPage() {
  const units = useUnits();
  const [rider, setRider] = useState(0);
  const [period, setPeriod] = useState<Period>('week');
  const [metric, setMetric] = useState<Metric>('distance_m');
  const [win, setWin] = useState<Window>('all');
  const riders = useQuery({
    queryKey: RIDERS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/riders')) as Rider[],
  });
  const rid = rider > 0 ? { rider_id: rider } : {};
  const totals = useQuery({
    queryKey: [...STATS_KEY, 'totals', rider, period],
    queryFn: async () => unwrap(await api.GET('/api/stats/totals', { params: { query: { period, tz, ...rid } } })),
  });
  const fitness = useQuery({
    queryKey: [...STATS_KEY, 'fitness', rider],
    queryFn: async () => unwrap(await api.GET('/api/stats/fitness', { params: { query: { days: 180, tz, ...rid } } })),
  });
  const curve = useQuery({
    queryKey: curveKey(rider, win),
    queryFn: async () => unwrap(await api.GET('/api/stats/power-curve', { params: { query: { window: win, ...rid } } })),
  });

  const m = METRICS.find((x) => x.key === metric) ?? METRICS[0];
  const bars = useMemo(
    () => (totals.data?.periods ?? []).map((p) => ({ label: barLabel(p.start, period), value: p[metric] })),
    [totals.data, metric, period],
  );
  const fmt = useMemo(() => (v: number) => m.fmt(v, units), [m, units]);
  const fitDays = useMemo(() => fitness.data?.days ?? [], [fitness.data]);
  const curvePts = useMemo(() => curve.data?.points ?? [], [curve.data]);

  const cur = totals.data?.periods[totals.data.periods.length - 1];
  const sk = totals.data?.streaks;
  const err = totals.error ?? fitness.error ?? curve.error;

  return (
    <div className="mx-auto flex w-full max-w-[100rem] flex-col gap-4">
      <div className="flex flex-wrap items-end gap-3">
        <h1 className="numeral min-w-0 flex-1 basis-40 text-2xl">Stats</h1>
        {(riders.data?.length ?? 0) > 1 && (
          <Select label="Rider" value={rider} onChange={(e) => setRider(Number(e.target.value))}>
            <option value={0}>All riders</option>
            {riders.data?.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </Select>
        )}
      </div>
      {err && <Banner tone="error">{errMessage(err)}</Banner>}

      <Card className="flex flex-col gap-4">
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
          <Tile label="Day streak">{sk ? plural(sk.current_days, 'day') : '-'}</Tile>
          <Tile label="Longest day streak">{sk ? plural(sk.longest_days, 'day') : '-'}</Tile>
          <Tile label="Week streak">{sk ? plural(sk.current_weeks, 'week') : '-'}</Tile>
          <Tile label="Longest week streak">{sk ? plural(sk.longest_weeks, 'week') : '-'}</Tile>
        </div>
        <div className="flex flex-wrap gap-3">
          <Select label="Period" value={period} onChange={(e) => setPeriod(e.target.value as Period)}>
            <option value="week">Weekly</option>
            <option value="month">Monthly</option>
            <option value="year">Yearly</option>
          </Select>
          <Select label="Measure" value={metric} onChange={(e) => setMetric(e.target.value as Metric)}>
            {METRICS.map((x) => (
              <option key={x.key} value={x.key}>
                {x.label}
              </option>
            ))}
          </Select>
        </div>
        {cur && (
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6" aria-label={`This ${period}`}>
            {METRICS.map((x) => (
              <Tile key={x.key} label={`This ${period}: ${x.label.toLowerCase()}`}>
                {x.fmt(cur[x.key], units)}
              </Tile>
            ))}
          </div>
        )}
        {totals.isPending ? (
          <p role="status" className="text-dim">
            Loading…
          </p>
        ) : (
          <TotalsChart title={`${m.label} per ${period}`} bars={bars} fmt={fmt} />
        )}
      </Card>

      <Card>
        {fitDays.length > 0 ? (
          <FitnessChart days={fitDays} />
        ) : (
          <p className="text-dim">{fitness.isPending ? 'Loading…' : 'Fitness appears after your first ride with a training stress score.'}</p>
        )}
      </Card>

      <Card className="flex flex-col gap-3">
        <div className="flex flex-wrap items-end gap-3">
          <Select label="Power curve window" value={win} onChange={(e) => setWin(e.target.value as Window)}>
            <option value="90d">Last 90 days</option>
            <option value="1y">Last year</option>
            <option value="all">All time</option>
          </Select>
          {curve.data?.eftp_w != null && (
            <p data-testid="eftp" className="numeral pb-3 text-lg">
              eFTP {Math.round(curve.data.eftp_w)} W
            </p>
          )}
        </div>
        {curvePts.length > 0 ? (
          <PowerCurve points={curvePts} />
        ) : (
          <p className="text-dim">{curve.isPending ? 'Loading…' : 'No power data in this window yet.'}</p>
        )}
      </Card>
    </div>
  );
}
