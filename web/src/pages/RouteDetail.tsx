import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Pencil, Play } from 'lucide-react';
import { api, ApiError, unwrap } from '@/lib/api';
import { useMe, useUnits } from '@/lib/auth';
import { fmtDist } from '@/lib/format';
import { errMessage } from '@/lib/errors';
import { getSelectedRiderId, setSelectedRiderId } from '@/lib/rider';
import { gradeAt, positionAt, type Profile, type RiderTune } from '@/lib/route-math';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { Field } from '@/components/Field';
import { Select } from '@/components/Select';
import MapView, { type MapApi } from '@/features/map/MapView';
import { AttemptsCard } from '@/features/routes/AttemptsCard';
import { PreviewChart } from '@/features/routes/PreviewChart';
import { ProfileCanvas } from '@/features/routes/ProfileCanvas';
import { profileKey, ROUTES_KEY, routeKey } from '@/features/routes/queries';
import { RouteStats, sourceLabel } from '@/features/routes/RouteStats';
import { RIDERS_KEY } from './Riders';

type Rider = components['schemas']['RiderOut'];
type Tune = Pick<Rider, 'gain' | 'offset_level' | 'downhill_scale' | 'lead_s'>;

const SLIDERS: { key: keyof Tune; label: string; min: number; max: number; step: number; unit: string }[] = [
  { key: 'gain', label: 'Gain', min: 0, max: 5, step: 0.1, unit: 'levels per %' },
  { key: 'offset_level', label: 'Flat-road level', min: 1, max: 32, step: 0.5, unit: '' },
  { key: 'downhill_scale', label: 'Downhill relief', min: 0, max: 1, step: 0.05, unit: '' },
  { key: 'lead_s', label: 'Lead', min: 0, max: 15, step: 0.5, unit: 's ahead' },
];

const isNotFound = (e: unknown) => e instanceof ApiError && e.status === 404;

function Gone() {
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3">
      <Banner tone="warn">This route no longer exists. It may have been deleted.</Banner>
      <Link to="/routes" className="inline-flex min-h-touch items-center px-1 text-accent">
        Back to routes
      </Link>
    </div>
  );
}

function Tuner({ profile }: { profile: Profile }) {
  const qc = useQueryClient();
  const riders = useQuery<Rider[]>({
    queryKey: RIDERS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/riders')),
  });
  const [chosen, setChosen] = useState<number | null>(getSelectedRiderId);
  const list = riders.data ?? [];
  const rider = list.find((r) => r.id === chosen) ?? list[0];

  const [edit, setEdit] = useState<Partial<Tune>>({});
  const [msg, setMsg] = useState<{ tone: 'ok' | 'error'; text: string } | null>(null);
  useEffect(() => {
    setEdit({});
    setMsg(null);
  }, [rider?.id, rider?.updated_at]);

  const save = useMutation({
    mutationFn: async (body: Partial<Tune>) => {
      if (!rider) throw new Error('No rider');
      return unwrap(await api.PATCH('/api/riders/{id}', { params: { path: { id: rider.id } }, body }));
    },
    onSuccess: () => {
      setMsg({ tone: 'ok', text: 'Saved to rider. It applies at the next connect.' });
      void qc.invalidateQueries({ queryKey: RIDERS_KEY });
    },
    onError: (e) => setMsg({ tone: 'error', text: errMessage(e) }),
  });

  if (riders.isPending) return <p role="status" className="text-dim">Loading riders…</p>;
  if (!rider) return <Banner tone="info">Add a rider on the Riders page to preview resistance.</Banner>;

  const cur: Tune = {
    gain: edit.gain ?? rider.gain,
    offset_level: edit.offset_level ?? rider.offset_level,
    downhill_scale: edit.downhill_scale ?? rider.downhill_scale,
    lead_s: edit.lead_s ?? rider.lead_s,
  };
  const changed: Partial<Tune> = {};
  for (const s of SLIDERS) if (cur[s.key] !== rider[s.key]) changed[s.key] = cur[s.key];
  const dirty = Object.keys(changed).length > 0;
  const tune: RiderTune = { ...cur, min_level: rider.min_level, max_level: rider.max_level };

  return (
    <div className="flex flex-col gap-4">
      <Select
        label="Rider"
        value={rider.id}
        onChange={(e) => {
          const id = Number(e.target.value);
          setChosen(id);
          setSelectedRiderId(id);
        }}
        hint={`Levels ${rider.min_level} to ${rider.max_level}`}
      >
        {list.map((r) => (
          <option key={r.id} value={r.id}>
            {r.name}
          </option>
        ))}
      </Select>

      <PreviewChart profile={profile} rider={tune} />

      <div className="grid gap-x-6 gap-y-3 sm:grid-cols-2">
        {SLIDERS.map((s) => (
          <label key={s.key} className="flex flex-col gap-1">
            <span className="flex items-baseline justify-between text-sm font-medium text-dim">
              {s.label}
              <span className="numeral text-lg text-chalk">
                {cur[s.key]} <span className="text-xs font-medium text-dim">{s.unit}</span>
              </span>
            </span>
            <input
              type="range"
              min={s.key === 'offset_level' ? Math.max(1, rider.min_level) : s.min}
              max={s.key === 'offset_level' ? rider.max_level : s.max}
              step={s.step}
              value={cur[s.key]}
              onChange={(e) => setEdit((p) => ({ ...p, [s.key]: Number(e.target.value) }))}
              className="h-12 w-full accent-[var(--accent)]"
            />
          </label>
        ))}
      </div>

      {msg && <Banner tone={msg.tone}>{msg.text}</Banner>}
      <div>
        <Button
          variant="primary"
          disabled={!dirty || save.isPending}
          onClick={() => {
            setMsg(null);
            save.mutate(changed);
          }}
        >
          Save to rider
        </Button>
      </div>
    </div>
  );
}

export function RouteDetailPage() {
  const id = Number(useParams().id);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const me = useMe();
  const units = useUnits();

  const route = useQuery({
    queryKey: routeKey(id),
    enabled: Number.isInteger(id) && id > 0,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/routes/{id}', { params: { path: { id } } })),
  });
  const profile = useQuery({
    queryKey: profileKey(id),
    enabled: Number.isInteger(id) && id > 0,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/routes/{id}/profile', { params: { path: { id } } })),
  });

  const [map, setMap] = useState<MapApi | null>(null);
  const r = route.data;
  useEffect(() => {
    if (!map || !r) return;
    map.setLine(r.geojson);
    if (r.bbox.length === 4) map.fit(r.bbox as [number, number, number, number]);
  }, [map, r]);

  const prof = profile.data;
  const [scrub, setScrub] = useState<number | null>(null);
  useEffect(() => {
    if (!map) return;
    map.setMarker(prof && scrub !== null ? positionAt(prof, scrub) : null);
  }, [map, prof, scrub]);

  const [editing, setEditing] = useState(false);
  const [name, setName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [confirm, setConfirm] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => () => clearTimeout(timer.current), []);

  const rename = useMutation({
    mutationFn: async () =>
      unwrap(await api.PATCH('/api/routes/{id}', { params: { path: { id } }, body: { name: name.trim() } })),
    onSuccess: () => {
      setEditing(false);
      void qc.invalidateQueries({ queryKey: ['routes'] });
    },
    onError: (e) => setError(errMessage(e)),
  });
  const del = useMutation({
    mutationFn: async () => unwrap(await api.DELETE('/api/routes/{id}', { params: { path: { id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ROUTES_KEY });
      navigate('/routes', { replace: true });
    },
    onError: (e) => {
      setConfirm(false);
      setError(errMessage(e));
    },
  });

  if (route.isPending) return <p role="status" className="text-dim">Loading…</p>;
  if (route.isError) {
    return isNotFound(route.error) ? <Gone /> : <Banner tone="error">{errMessage(route.error)}</Banner>;
  }
  const rt = route.data;
  const canEdit = !!me.data && (me.data.user.is_admin || me.data.user.id === rt.owner_id);
  const synthetic = rt.source === 'synthetic';

  return (
    <div className="mx-auto flex w-full max-w-[100rem] flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        {editing ? (
          <form
            className="flex min-w-0 flex-1 flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              setError(null);
              rename.mutate();
            }}
          >
            <div className="min-w-48 flex-1">
              <Field label="Route name" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />
            </div>
            <Button type="submit" variant="primary" disabled={!name.trim() || rename.isPending}>
              Save name
            </Button>
            <Button onClick={() => setEditing(false)}>Cancel</Button>
          </form>
        ) : (
          <>
            <h1 className="numeral min-w-0 flex-1 truncate text-2xl">{rt.name}</h1>
            <span className="rounded-full border border-line px-2.5 py-0.5 text-xs text-dim">
              {sourceLabel(rt.source)}
            </span>
            {canEdit && (
              <Button
                aria-label="Rename route"
                onClick={() => {
                  setName(rt.name);
                  setEditing(true);
                }}
              >
                <Pencil aria-hidden className="size-5" />
                Rename
              </Button>
            )}
          </>
        )}
      </div>

      {error && <Banner tone="error">{error}</Banner>}

      <div className={synthetic ? 'grid gap-4' : 'grid gap-4 lg:grid-cols-[minmax(0,1.7fr)_minmax(0,1fr)]'}>
        {!synthetic && <MapView data-testid="route-map" className="h-72 md:h-96 lg:h-[26rem]" onReady={setMap} />}
        <Card className="flex flex-col justify-between gap-5">
          <RouteStats route={rt} units={units} />
          <div className="flex flex-wrap gap-3">
            <Link
              to={`/?route=${rt.id}`}
              className="inline-flex min-h-touch items-center justify-center gap-2 rounded-control bg-accent px-5 text-lg font-medium text-accent-ink hover:brightness-110"
            >
              <Play aria-hidden className="size-5" />
              Ride this route
            </Link>
            {canEdit && (
              <Button
                variant={confirm ? 'danger' : 'secondary'}
                disabled={del.isPending}
                onClick={() => {
                  setError(null);
                  if (confirm) {
                    clearTimeout(timer.current);
                    del.mutate();
                  } else {
                    setConfirm(true);
                    timer.current = setTimeout(() => setConfirm(false), 4000);
                  }
                }}
              >
                {confirm ? 'Tap again to delete' : 'Delete'}
              </Button>
            )}
          </div>
        </Card>
      </div>

      <AttemptsCard routeId={id} units={units} />

      <Card className="flex flex-col gap-3">
        <h2 className="numeral text-xl">Elevation</h2>
        {profile.isPending ? (
          <p role="status" className="text-dim">Loading profile…</p>
        ) : profile.isError ? (
          <Banner tone={isNotFound(profile.error) ? 'warn' : 'error'}>
            {isNotFound(profile.error) ? 'This route has no elevation profile any more.' : errMessage(profile.error)}
          </Banner>
        ) : (
          <>
            <ProfileCanvas units={units} profile={profile.data} onScrub={synthetic ? undefined : setScrub} height={200} />
            <p className="numeral h-7 text-lg text-dim" aria-live="off">
              {scrub !== null
                ? `${fmtDist(scrub, units)} · ${gradeAt(profile.data, scrub).toFixed(1)} %`
                : ''}
            </p>
          </>
        )}
      </Card>

      {prof && (
        <Card className="flex flex-col gap-3">
          <h2 className="numeral text-xl">Resistance preview</h2>
          <Tuner profile={prof} />
        </Card>
      )}
    </div>
  );
}
