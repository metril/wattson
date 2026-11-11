import { useState } from 'react';
import { Link } from 'react-router';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { RIDES_KEY } from '@/lib/rides';
import { useUnits } from '@/lib/auth';
import { clockTime, distUnit, fixed0, hms, relDate, toDist, type Units } from '@/lib/format';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Badge } from '@/components/Badge';
import { Card } from '@/components/Card';
import { PageHeader } from '@/components/PageHeader';
import { SectionHeader } from '@/components/SectionHeader';
import { StatTile } from '@/components/StatTile';
import { STATS_KEY } from '@/features/stats/queries';
import { Select } from '@/components/Select';
import { RIDERS_KEY } from './Riders';

type Rider = components['schemas']['RiderOut'];
type Ride = components['schemas']['RideOut'];

const RIDER_KEY = 'wattson.history_rider';
const PAGE = 20;

function storedRider(): number {
  try {
    const n = Number(localStorage.getItem(RIDER_KEY));
    return Number.isInteger(n) && n > 0 ? n : 0;
  } catch {
    return 0;
  }
}

function storeRider(id: number): void {
  try {
    if (id > 0) localStorage.setItem(RIDER_KEY, String(id));
    else localStorage.removeItem(RIDER_KEY);
  } catch {
    // storage unavailable; the choice just does not persist
  }
}

const COLS = 'md:grid-cols-[7.5rem_minmax(0,0.7fr)_minmax(0,1.3fr)_5.5rem_4.5rem_3.5rem_3.5rem_3.5rem] lg:grid-cols-[8rem_minmax(0,0.7fr)_minmax(0,1.3fr)_5.5rem_4.5rem_3.5rem_3.5rem_3.5rem_3.5rem_5rem]';
const MS = 86_400_000;

function Stat({ label, children, className = '' }: { label: string; children: string; className?: string }) {
  return (
    <span className={`flex min-w-0 items-baseline justify-between gap-2 rounded-control bg-raised px-3 py-1.5 md:block md:rounded-none md:bg-transparent md:p-0 md:text-right ${className}`}>
      <span className="text-xs text-dim md:hidden">{label}</span>
      <span className="numeral text-sm md:text-sm">{children}</span>
    </span>
  );
}

function ZoneMix({ zones }: { zones: number[] | null }) {
  const total = zones?.reduce((a, b) => a + b, 0) ?? 0;
  if (!zones || total <= 0) return <span className="hidden lg:block" />;
  return (
    <span aria-hidden className="hidden h-1.5 gap-px overflow-hidden rounded-full bg-sunken lg:flex">
      {zones.map((z, i) => (
        <span key={i} className="block h-full" style={{ flexGrow: z, flexBasis: 0, background: `var(--zone-${i + 1})` }} />
      ))}
    </span>
  );
}

function Row({ r, units }: { r: Ride; units: Units }) {
  return (
    <li>
      <Link
        to={`/history/${r.id}`}
        data-nav
        className={`grid min-h-[2.75rem] grid-cols-2 items-center gap-x-3 gap-y-2 px-4 py-3 transition-colors hover:bg-raised md:py-1.5 ${COLS}`}
      >
        <span className="flex min-w-0 flex-col leading-tight">
          <span className="text-sm font-medium">{relDate(r.started_at)}</span>
          <span className="text-xs text-dim">{clockTime(r.started_at)}</span>
        </span>
        <span className="min-w-0 justify-self-end md:justify-self-start">
          <span className="block max-w-full truncate rounded-full bg-raised px-2.5 py-0.5 text-xs text-dim md:bg-sunken">{r.rider_name}</span>
        </span>
        <span className="col-span-2 flex min-w-0 items-center gap-2 md:col-span-1">
          <span className="truncate font-medium">{r.route_name || 'Free ride'}</span>
          {r.strava.status === 'done' && <Badge tone="ok">Strava</Badge>}
        </span>
        <Stat label="Time">{hms(r.duration_s)}</Stat>
        <Stat label={distUnit(units)}>{toDist(r.distance_m, units).toFixed(2)}</Stat>
        <Stat label="Avg W">{fixed0(r.avg_power_w)}</Stat>
        <Stat label="NP">{fixed0(r.np_w)}</Stat>
        <Stat label="kJ" className="hidden md:block">{fixed0(r.kj)}</Stat>
        <Stat label="TSS" className="hidden lg:block">{r.tss != null ? fixed0(r.tss) : '–'}</Stat>
        <ZoneMix zones={r.power_zone_s} />
      </Link>
    </li>
  );
}

/** Totals of the last 30 days, from a single page of up to 100 rides. */
function Summary({ riderId, units, enabled }: { riderId: number; units: Units; enabled: boolean }) {
  const q = useQuery({
    queryKey: [...STATS_KEY, 'history-30d', riderId],
    enabled,
    queryFn: async () => unwrap(await api.GET('/api/rides', { params: { query: { ...(riderId ? { rider_id: riderId } : {}), limit: 100 } } })),
  });
  if (!q.data) return null;
  const since = Date.now() - 30 * MS;
  const rs = q.data.rides.filter((r) => new Date(r.started_at).getTime() >= since);
  if (rs.length === 0) return null;
  const sum = (f: (r: Ride) => number) => rs.reduce((a, r) => a + f(r), 0);
  const more = q.data.next_before > 0 && rs.length === q.data.rides.length;
  return (
    <Card aria-label="Last 30 days">
      <SectionHeader title="Last 30 days" />
      <dl className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-5">
        <StatTile label="Rides" value={`${rs.length}${more ? '+' : ''}`} />
        <StatTile label="Time" value={hms(sum((r) => r.duration_s))} />
        <StatTile label="Distance" value={toDist(sum((r) => r.distance_m), units).toFixed(1)} unit={distUnit(units)} color="speed" />
        <StatTile label="Energy" value={fixed0(sum((r) => r.kj))} unit="kJ" color="power" />
        <StatTile label="TSS" value={fixed0(sum((r) => r.tss ?? 0))} />
      </dl>
    </Card>
  );
}

export function HistoryPage() {
  const units = useUnits();
  const riders = useQuery<Rider[]>({
    queryKey: RIDERS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/riders')),
  });
  const [chosen, setChosen] = useState(storedRider);
  // A stale choice (rider deleted) falls back to everyone.
  const riderId = riders.data?.some((r) => r.id === chosen) ? chosen : 0;

  const rides = useInfiniteQuery({
    queryKey: [...RIDES_KEY, riderId],
    enabled: riders.isSuccess,
    initialPageParam: 0,
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET('/api/rides', {
          params: {
            query: {
              ...(riderId ? { rider_id: riderId } : {}),
              ...(pageParam ? { before: pageParam } : {}),
              limit: PAGE,
            },
          },
        }),
      ),
    getNextPageParam: (last) => (last.next_before > 0 ? last.next_before : undefined),
  });
  const list = rides.data?.pages.flatMap((p) => p.rides) ?? [];

  return (
    <div className="mx-auto flex w-full max-w-6xl flex-col gap-4">
      <PageHeader
        title="History"
        subtitle="Every finished ride"
        actions={
          <>
            <Link to="/stats" className="flex min-h-touch items-center rounded-control px-3 font-medium text-accent hover:bg-raised">
              Stats
            </Link>
            {(riders.data?.length ?? 0) > 1 && (
              <div className="w-full sm:w-56">
                <Select
                  label="Rider"
                  value={riderId}
                  onChange={(e) => {
                    const id = Number(e.target.value);
                    setChosen(id);
                    storeRider(id);
                  }}
                >
                  <option value={0}>All my riders</option>
                  {riders.data?.map((r) => (
                    <option key={r.id} value={r.id}>
                      {r.name}
                    </option>
                  ))}
                </Select>
              </div>
            )}
          </>
        }
      />
      {riders.isSuccess && <Summary riderId={riderId} units={units} enabled={rides.isSuccess} />}
      {riders.isError ? (
        <Banner tone="error">{errMessage(riders.error)}</Banner>
      ) : rides.isError ? (
        <Banner tone="error">{errMessage(rides.error)}</Banner>
      ) : rides.isPending ? (
        <p role="status" className="text-dim">
          Loading…
        </p>
      ) : list.length === 0 ? (
        <Card>
          <p className="text-dim">No rides yet. Finished rides appear here.</p>
        </Card>
      ) : (
        <>
          <Card className="overflow-hidden p-0">
            <div
              aria-hidden
              className={`hidden gap-x-3 border-b border-line px-4 py-2 text-xs font-medium tracking-wide text-label uppercase md:grid ${COLS}`}
            >
              <span>Date</span>
              <span>Rider</span>
              <span>Route</span>
              <span className="text-right">Time</span>
              <span className="text-right">{distUnit(units)}</span>
              <span className="text-right">Avg W</span>
              <span className="text-right">NP</span>
              <span className="text-right">kJ</span>
              <span className="hidden text-right lg:block">TSS</span>
              <span className="hidden lg:block">Zones</span>
            </div>
            <ul className="divide-y divide-line-soft">
              {list.map((r) => (
                <Row key={r.id} r={r} units={units} />
              ))}
            </ul>
          </Card>
          {rides.hasNextPage && (
            <div>
              <Button disabled={rides.isFetchingNextPage} onClick={() => void rides.fetchNextPage()}>
                Load more
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
