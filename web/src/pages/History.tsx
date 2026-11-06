import { useState } from 'react';
import { Link } from 'react-router';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { RIDES_KEY } from '@/lib/rides';
import { useUnits } from '@/lib/auth';
import { distUnit, fixed0, hms, rideDate, toDist, type Units } from '@/lib/format';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
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

const COLS = 'md:grid-cols-[minmax(9rem,1.2fr)_minmax(5rem,0.8fr)_minmax(6rem,1.2fr)_5.5rem_4rem_4rem_4rem_4rem_4.5rem]';

function Stat({ label, children }: { label: string; children: string }) {
  return (
    <span className="flex min-w-0 flex-col md:block">
      <span className="text-xs text-dim md:hidden">{label}</span>
      <span className="numeral text-base">{children}</span>
    </span>
  );
}

function Row({ r, units }: { r: Ride; units: Units }) {
  return (
    <li>
      <Link
        to={`/history/${r.id}`}
        data-nav
        className={`grid min-h-touch grid-cols-3 items-center gap-x-3 gap-y-2 px-4 py-3 hover:bg-raised md:grid-cols-none ${COLS}`}
      >
        <span className="col-span-2 min-w-0 md:col-span-1">
          <span className="block text-sm text-dim">{rideDate(r.started_at)}</span>
        </span>
        <span className="truncate text-right text-sm text-dim md:text-left">{r.rider_name}</span>
        <span className="col-span-3 min-w-0 truncate font-medium md:col-span-1">{r.route_name || 'Free ride'}</span>
        <Stat label="Time">{hms(r.duration_s)}</Stat>
        <Stat label={distUnit(units)}>{toDist(r.distance_m, units).toFixed(2)}</Stat>
        <Stat label="Avg W">{fixed0(r.avg_power_w)}</Stat>
        <Stat label="NP">{fixed0(r.np_w)}</Stat>
        <Stat label="kJ">{fixed0(r.kj)}</Stat>
        <span className="text-sm">
          {r.strava.status === 'done' && (
            <span className="rounded-full border border-ok px-2.5 py-0.5 text-xs text-ok">Strava</span>
          )}
        </span>
      </Link>
    </li>
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
      <div className="flex flex-wrap items-end gap-3">
        <h1 className="numeral flex-1 text-2xl">History</h1>
        {(riders.data?.length ?? 0) > 1 && (
          <div className="w-full sm:w-64">
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
      </div>
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
              className={`hidden gap-x-4 border-b border-line px-4 py-2 text-xs text-dim md:grid ${COLS}`}
            >
              <span>Date</span>
              <span>Rider</span>
              <span>Route</span>
              <span>Time</span>
              <span>{distUnit(units)}</span>
              <span>Avg W</span>
              <span>NP</span>
              <span>kJ</span>
              <span />
            </div>
            <ul className="divide-y divide-line">
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
