import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Field } from '@/components/Field';
import { Select } from '@/components/Select';
import { api, unwrap } from '@/lib/api';
import { MAX_TARGET_W, MIN_TARGET_W } from '@/lib/workout-math';
import { ROUTES_KEY } from '@/features/routes/queries';
import { WORKOUTS_KEY } from '@/features/workouts/queries';

export type StartChoice =
  | { kind: 'free' }
  | { kind: 'route'; id: number }
  | { kind: 'workout'; id: number }
  | { kind: 'erg'; watts: number };

export const DEFAULT_ERG_W = 150;

const positive = (s: string | null): number | null => {
  const n = Number(s);
  return Number.isInteger(n) && n > 0 ? n : null;
};

/** What the ride screen starts with: ?workout= or ?route= when given, else a free ride. */
export function choiceFromParams(p: URLSearchParams): StartChoice {
  const workout = positive(p.get('workout'));
  if (workout !== null) return { kind: 'workout', id: workout };
  const route = positive(p.get('route'));
  return route !== null ? { kind: 'route', id: route } : { kind: 'free' };
}

/** The error for a free-ERG target outside the accepted range, or null. */
export function ergError(watts: number): string | null {
  return Number.isInteger(watts) && watts >= MIN_TARGET_W && watts <= MAX_TARGET_W
    ? null
    : `Target power must be a whole number from ${MIN_TARGET_W} to ${MAX_TARGET_W} W.`;
}

const encode = (c: StartChoice) => (c.kind === 'route' || c.kind === 'workout' ? `${c.kind}:${c.id}` : c.kind);

/**
 * What the next Start rides: a free ride, a saved route, a saved workout or
 * free ERG at a fixed power. A route or workout that no longer exists falls
 * back to a free ride.
 */
export function StartPicker({ value, onChange }: { value: StartChoice; onChange(c: StartChoice): void }) {
  const routes = useQuery({
    queryKey: ROUTES_KEY,
    staleTime: Infinity,
    queryFn: async () => unwrap(await api.GET('/api/routes')),
  });
  const workouts = useQuery({
    queryKey: WORKOUTS_KEY,
    staleTime: Infinity,
    queryFn: async () => unwrap(await api.GET('/api/workouts')),
  });
  const routeList = routes.data ?? [];
  const workoutList = workouts.data ?? [];
  // Judge only fresh lists: right after a save, the cached list is invalidated and still lacks the new item.
  const missing =
    (value.kind === 'route' && !!routes.data && !routes.isFetching && !routeList.some((r) => r.id === value.id)) ||
    (value.kind === 'workout' && !!workouts.data && !workouts.isFetching && !workoutList.some((w) => w.id === value.id));
  // A list that failed to load before ever having data is unknown, not empty: do not keep an id we cannot check.
  const unknown =
    (value.kind === 'route' && routes.isError && !routes.data) || (value.kind === 'workout' && workouts.isError && !workouts.data);
  const [notice, setNotice] = useState<string | null>(null);
  useEffect(() => {
    if (missing || unknown) {
      if (unknown) setNotice(`Could not load the ${value.kind === 'route' ? 'routes' : 'workouts'}, so Free ride is selected.`);
      onChange({ kind: 'free' });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [missing, unknown]);

  function pick(v: string) {
    if (v === 'erg') onChange({ kind: 'erg', watts: DEFAULT_ERG_W });
    else if (v.startsWith('route:')) onChange({ kind: 'route', id: Number(v.slice(6)) });
    else if (v.startsWith('workout:')) onChange({ kind: 'workout', id: Number(v.slice(8)) });
    else onChange({ kind: 'free' });
  }

  return (
    <div className="flex flex-col gap-3">
      {notice && (
        <p role="status" className="text-sm text-warn">
          {notice}
        </p>
      )}
      <Select label="Ride" value={missing || unknown ? 'free' : encode(value)} onChange={(e) => pick(e.target.value)}>
        <option value="free">Free ride</option>
        <option value="erg">Free ERG (hold a power)</option>
        {routeList.length > 0 && (
          <optgroup label="Routes">
            {routeList.map((r) => (
              <option key={r.id} value={`route:${r.id}`}>
                {r.name}
              </option>
            ))}
          </optgroup>
        )}
        {workoutList.length > 0 && (
          <optgroup label="Workouts">
            {workoutList.map((w) => (
              <option key={w.id} value={`workout:${w.id}`}>
                {w.name}
              </option>
            ))}
          </optgroup>
        )}
      </Select>
      {value.kind === 'erg' && (
        <Field
          label="Target power (W)"
          type="number"
          inputMode="numeric"
          min={MIN_TARGET_W}
          max={MAX_TARGET_W}
          value={Number.isNaN(value.watts) ? '' : value.watts}
          onChange={(e) => onChange({ kind: 'erg', watts: e.target.value === '' ? NaN : Number(e.target.value) })}
          onKeyDown={(e) => {
            // Enter commits the value and moves on to Start (arrows stay with the number field).
            if (e.key !== 'Enter') return;
            e.preventDefault();
            document.querySelector<HTMLElement>('[data-primary]:not(:disabled)')?.focus();
          }}
          error={ergError(value.watts) ?? undefined}
        />
      )}
    </div>
  );
}
