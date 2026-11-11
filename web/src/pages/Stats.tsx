import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { useUnits } from '@/lib/auth';
import { fmtDist, fmtElev, hms, type Units } from '@/lib/format';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Card } from '@/components/Card';
import { PageHeader } from '@/components/PageHeader';
import { Segmented } from '@/components/Segmented';
import { SectionHeader } from '@/components/SectionHeader';
import { Select } from '@/components/Select';
import { StatTile } from '@/components/StatTile';
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

type Form = { word: 'Fresh' | 'Neutral' | 'Fatigued'; tone: 'ok' | 'accent' | 'warn'; blurb: string };
/** Training-stress balance in words: positive means rested, deeply negative means carrying fatigue. */
export function formOf(tsb: number): Form {
  if (tsb >= 5) return { word: 'Fresh', tone: 'ok', blurb: 'Rested and ready for a hard effort or race.' };
  if (tsb <= -10) return { word: 'Fatigued', tone: 'warn', blurb: 'Carrying fatigue. Easy days will bring it back.' };
  return { word: 'Neutral', tone: 'accent', blurb: 'Balanced between fitness and fatigue.' };
}
const TONE_TEXT = { ok: 'text-ok', accent: 'text-accent', warn: 'text-warn' } as const;

function FormCard({ day }: { day?: { ctl: number; atl: number; tsb: number } }) {
  if (!day) {
    return (
      <Card className="flex flex-col gap-2">
        <SectionHeader title="Form" />
        <p className="text-dim">Form appears after your first ride with a training stress score.</p>
      </Card>
    );
  }
  const f = formOf(day.tsb);
  return (
    <Card className="flex flex-col gap-4" data-testid="form-card">
      <SectionHeader title="Form" hint="Fitness minus fatigue, today" />
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
        <p className={`numeral text-5xl leading-none ${TONE_TEXT[f.tone]}`}>{f.word}</p>
        <p className="text-sm text-dim">{f.blurb}</p>
      </div>
      <dl className="grid grid-cols-3 gap-4">
        <StatTile label="Fitness (CTL)" value={day.ctl.toFixed(0)} color="speed" />
        <StatTile label="Fatigue (ATL)" value={day.atl.toFixed(0)} color="hr" />
        <StatTile label="Form (TSB)" value={`${day.tsb > 0 ? '+' : ''}${day.tsb.toFixed(0)}`} />
      </dl>
    </Card>
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

  const latestDay = fitDays[fitDays.length - 1];

  return (
    <div className="mx-auto flex w-full max-w-[100rem] flex-col gap-4">
      <PageHeader
        title="Stats"
        subtitle="Training load, totals and power"
        actions={
          (riders.data?.length ?? 0) > 1 && (
            <Select label="Rider" value={rider} onChange={(e) => setRider(Number(e.target.value))}>
              <option value={0}>All riders</option>
              {riders.data?.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </Select>
          )
        }
      />
      {err && <Banner tone="error">{errMessage(err)}</Banner>}

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
        <FormCard day={latestDay} />
        <Card>
          <SectionHeader title="Streaks" />
          <div className="grid grid-cols-2 gap-2">
            <Tile label="Day streak">{sk ? plural(sk.current_days, 'day') : '-'}</Tile>
            <Tile label="Longest day streak">{sk ? plural(sk.longest_days, 'day') : '-'}</Tile>
            <Tile label="Week streak">{sk ? plural(sk.current_weeks, 'week') : '-'}</Tile>
            <Tile label="Longest week streak">{sk ? plural(sk.longest_weeks, 'week') : '-'}</Tile>
          </div>
        </Card>
      </div>

      <Card className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center gap-3">
          <Segmented
            label="Period"
            value={period}
            onChange={setPeriod}
            options={[
              { value: 'week', label: 'Weekly' },
              { value: 'month', label: 'Monthly' },
              { value: 'year', label: 'Yearly' },
            ]}
          />
          <div className="max-w-full overflow-x-auto">
            <Segmented label="Measure" value={metric} onChange={setMetric} options={METRICS.map((x) => ({ value: x.key, label: x.label }))} />
          </div>
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
        <div className="flex flex-wrap items-center gap-3">
          <Segmented
            label="Power curve window"
            value={win}
            onChange={setWin}
            options={[
              { value: '90d', label: '90 days' },
              { value: '1y', label: '1 year' },
              { value: 'all', label: 'All time' },
            ]}
          />
          {curve.data?.eftp_w != null && (
            <p data-testid="eftp" className="numeral text-lg">
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
