import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Select } from '@/components/Select';
import { api, ApiError, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { getSelectedRiderId, setSelectedRiderId } from '@/lib/rider';
import type { TickStore } from '@/lib/tick-store';
import type { BikeStatus, RideState } from '@/lib/ws-types';
import { RIDERS_KEY } from '@/pages/Riders';

const stateStyle: Record<RideState, string> = {
  idle: 'border-line text-dim',
  connecting: 'border-accent text-accent',
  ready: 'border-accent text-chalk',
  riding: 'border-ok text-ok',
  paused: 'border-warn text-warn',
  finished: 'border-line text-chalk',
};

/**
 * Top strip of the ride screen: who rides, connect and release, the link
 * state, and every warning about the bike. Rider safety first: a bike that
 * may still be at a high level is shown in an error banner.
 */
export function BikeBar({
  bike,
  connected,
  state,
  ticks,
}: {
  bike: BikeStatus | null;
  connected: boolean;
  state: RideState;
  ticks: TickStore;
}) {
  const riders = useQuery({
    queryKey: RIDERS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/riders')),
  });
  const [picked, setPicked] = useState(getSelectedRiderId);
  const [floorLevel, setFloorLevel] = useState<number | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const list = riders.data ?? [];
  const idle = state === 'idle';
  const bound = !idle ? bike?.rider_id : undefined;
  const riderId = [bound, picked].find((id) => id != null && list.some((r) => r.id === id)) ?? list[0]?.id ?? '';

  const connect = useMutation({
    mutationFn: async (rider_id: number) => unwrap(await api.POST('/api/bike/connect', { body: { rider_id } })),
    onMutate: () => {
      setFloorLevel(null);
      setFailure(null);
    },
    onError: (e) => setFailure(errMessage(e)),
  });

  const release = useMutation({
    mutationFn: async () => unwrap(await api.POST('/api/bike/release')),
    onMutate: () => setFailure(null),
    onError: (e) => {
      if (e instanceof ApiError && e.code === 'floor_unconfirmed') {
        const t = ticks.latest;
        setFloorLevel(t ? t.reported_level || t.level : 0);
      } else {
        setFailure(errMessage(e));
      }
    },
  });

  const lastError = bike?.last_error;
  const floorBike = !!bike?.floor_unconfirmed;
  // One banner for the floor warning: the server's status wins, the client-side
  // 424 level only covers the moment before that status arrives.
  const floorWarn = floorBike || floorLevel !== null;
  const known = ticks.latest ? ticks.latest.reported_level || ticks.latest.level : 0;
  const floorN = floorBike ? known || floorLevel : floorLevel;

  return (
    <section aria-label="Bike" className="flex flex-col gap-3">
      <div className="flex flex-wrap items-end gap-x-6 gap-y-3 rounded-panel bg-panel p-3 md:p-4">
        <div className="flex min-w-0 flex-col gap-1">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm md:text-base">
            <span
              className={`numeral rounded-control border-2 px-3 py-0.5 text-xl capitalize ${stateStyle[state]}`}
              data-testid="ride-state"
            >
              {state}
            </span>
            {bike?.protocol && <span className="text-dim">Protocol {bike.protocol}</span>}
            <span className="text-dim">{connected ? 'Server link up' : 'Server link down'}</span>
          </div>
        </div>
        <div className="flex w-full items-end gap-3 md:ml-auto md:w-auto">
          <div className="min-w-0 flex-1 md:w-48 md:flex-none">
            <Select
              label="Rider"
              value={riderId}
              disabled={!idle || list.length === 0}
              onChange={(e) => {
                const id = Number(e.target.value);
                setPicked(id);
                setSelectedRiderId(id);
              }}
            >
              {list.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </Select>
          </div>
          {idle ? (
            <Button
              variant="primary"
              data-bike-toggle
              disabled={typeof riderId !== 'number' || connect.isPending}
              onClick={() => typeof riderId === 'number' && connect.mutate(riderId)}
            >
              Connect
            </Button>
          ) : (
            <Button variant="secondary" data-bike-toggle disabled={release.isPending} onClick={() => release.mutate()}>
              Release
            </Button>
          )}
        </div>
      </div>

      {!connected && (
        <Banner tone="warn">
          <p className="font-medium">Reconnecting…</p>
          <p className="text-sm text-dim">Ride controls are off until the link is back.</p>
        </Banner>
      )}
      {floorWarn ? (
        <Banner tone="error">
          <p className="font-medium">
            {floorN != null && floorN > 0
              ? `Bike may still be at level ${floorN}`
              : 'Bike may still be at a high level'}{' '}
            — lower the resistance at the console.
          </p>
          {floorBike && lastError && <p className="text-sm text-dim">{lastError}</p>}
        </Banner>
      ) : (
        lastError && (
          <Banner tone="error">
            <p className="font-medium">{lastError}</p>
            {bike?.hint && <p className="text-sm text-dim">{bike.hint}</p>}
          </Banner>
        )
      )}
      {failure && <Banner tone="error">{failure}</Banner>}
    </section>
  );
}
