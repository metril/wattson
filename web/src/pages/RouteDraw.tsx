import { useEffect, useMemo, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Crosshair, Eraser, Trash2, Undo2 } from 'lucide-react';
import { api, ApiError, unwrap } from '@/lib/api';
import { useMe, useUnits } from '@/lib/auth';
import { errMessage } from '@/lib/errors';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { Field } from '@/components/Field';
import { Select } from '@/components/Select';
import MapView, { type MapApi } from '@/features/map/MapView';
import { ProfileCanvas } from '@/features/routes/ProfileCanvas';
import { ROUTES_KEY, routeKey } from '@/features/routes/queries';
import { RouteStats } from '@/features/routes/RouteStats';

type Draw = components['schemas']['DrawOut'];
type Profile = NonNullable<components['schemas']['DrawInBody']['profile']>;
type LL = [number, number];

const MAX_WAYPOINTS = 30;
const DEBOUNCE_MS = 400;
const PROFILES: { value: Profile; label: string }[] = [
  { value: 'trekking', label: 'Trekking' },
  { value: 'fastbike', label: 'Fast bike' },
  { value: 'safety', label: 'Safety' },
  { value: 'shortest', label: 'Shortest' },
];

type Failure = { message: string; missing: string[] };

const failure = (e: unknown): Failure => ({
  message: errMessage(e),
  missing: e instanceof ApiError && e.code === 'missing_tiles' ? (e.details?.missing_tiles ?? []) : [],
});

function parseView(params: URLSearchParams): { center: LL; zoom: number; explicit: boolean } {
  const c = params.get('c')?.split(',').map(Number);
  const z = Number(params.get('z'));
  if (c && c.length === 2 && c.every(Number.isFinite) && Math.abs(c[0]) <= 180 && Math.abs(c[1]) <= 90) {
    return { center: [c[0], c[1]], zoom: Number.isFinite(z) && z > 0 && z <= 22 ? z : 10, explicit: true };
  }
  return { center: [7.5, 45.5], zoom: 5, explicit: false };
}

function MissingTiles({ tiles, admin }: { tiles: string[]; admin: boolean }) {
  const download = useMutation({
    mutationFn: async () => {
      const res = await Promise.allSettled(
        tiles.map(async (name) => unwrap(await api.POST('/api/maps/tiles/{name}', { params: { path: { name } } }))),
      );
      const failed = res.flatMap((r, i) => (r.status === 'rejected' ? [`${tiles[i]}: ${errMessage(r.reason)}`] : []));
      if (failed.length) throw new Error(failed.join('; '));
    },
  });
  const list = tiles.join(', ');
  return (
    <Banner
      tone="warn"
      action={
        admin && !download.isSuccess ? (
          <Button disabled={download.isPending} onClick={() => download.mutate()}>
            Download these regions
          </Button>
        ) : undefined
      }
    >
      <p>
        Map data missing for {list} —{' '}
        {admin ? (
          <>
            download {tiles.length === 1 ? 'it' : 'them'} in{' '}
            <Link to="/settings/maps" className="text-accent underline">
              Map regions
            </Link>
          </>
        ) : (
          'ask an admin to download it in Map regions'
        )}
      </p>
      {download.isSuccess && (
        <p className="text-sm text-dim">Download started. Draw again when it finishes; progress is in Map regions.</p>
      )}
      {download.isError && <p className="text-sm text-danger">{errMessage(download.error)}</p>}
    </Banner>
  );
}

export function RouteDrawPage() {
  const units = useUnits();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [params] = useSearchParams();
  const view = useMemo(() => parseView(params), [params]);
  const me = useMe();
  const isAdmin = !!me.data?.user.is_admin;

  // Without an explicit view, open on the newest route.
  const routes = useQuery({
    queryKey: ROUTES_KEY,
    enabled: !view.explicit,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/routes')),
  });
  const lastId = routes.data?.[0]?.id;
  const last = useQuery({
    queryKey: routeKey(lastId ?? 0),
    enabled: !view.explicit && lastId !== undefined,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/routes/{id}', { params: { path: { id: lastId ?? 0 } } })),
  });

  const [map, setMap] = useState<MapApi | null>(null);
  const [waypoints, setWaypoints] = useState<LL[]>([]);
  const [profileChoice, setProfileChoice] = useState<Profile | null>(null);
  const [name, setName] = useState('');
  // The inputs a preview was computed for, to tell when it has gone stale.
  const [preview, setPreview] = useState<(Draw & { wps: LL[]; prof: Profile | null }) | null>(null);
  const [busy, setBusy] = useState(false);
  const [fail, setFail] = useState<Failure | null>(null);
  const [retry, setRetry] = useState(0);

  // null leaves the choice to the server (Settings, Integrations).
  const profile = profileChoice;

  const bbox = last.data?.bbox;
  useEffect(() => {
    if (map && bbox && bbox.length === 4 && waypoints.length === 0) map.fit(bbox as [number, number, number, number]);
    // Only the first fit: later waypoints must not be fought by a late-arriving route.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [map, bbox]);

  useEffect(() => {
    map?.setWaypoints(waypoints);
  }, [map, waypoints]);
  useEffect(() => {
    map?.setLine(preview?.route.geojson ?? null);
  }, [map, preview]);

  // Preview: a quiet 400 ms after the last change, with any earlier request cancelled.
  useEffect(() => {
    if (waypoints.length < 2) {
      setPreview(null);
      setFail(null);
      setBusy(false);
      return;
    }
    const ac = new AbortController();
    const t = setTimeout(async () => {
      setBusy(true);
      setFail(null);
      try {
        const d = unwrap(
          await api.POST('/api/routes/draw', {
            body: { waypoints, ...(profile ? { profile } : {}), dry_run: true },
            signal: ac.signal,
          }),
        );
        if (ac.signal.aborted) return;
        setPreview({ ...d, wps: waypoints, prof: profile });
        setBusy(false);
      } catch (e) {
        if (ac.signal.aborted) return;
        setPreview(null);
        setFail(failure(e));
        setBusy(false);
      }
    }, DEBOUNCE_MS);
    return () => {
      clearTimeout(t);
      ac.abort();
    };
  }, [waypoints, profile, retry]);

  const save = useMutation({
    mutationFn: async () =>
      unwrap(
        await api.POST('/api/routes/draw', {
          body: { waypoints, ...(profile ? { profile } : {}), dry_run: false, ...(name.trim() ? { name: name.trim() } : {}) },
        }),
      ),
    onSuccess: (d) => {
      void qc.invalidateQueries({ queryKey: ROUTES_KEY });
      navigate(`/routes/${d.route.id}`);
    },
    onError: (e) => setFail(failure(e)),
  });

  const add = (ll: LL) => setWaypoints((w) => (w.length >= MAX_WAYPOINTS ? w : [...w, ll]));
  const change = setWaypoints;
  const full = waypoints.length >= MAX_WAYPOINTS;
  const stale = !!preview && (preview.wps !== waypoints || preview.prof !== profile);

  return (
    <div className="mx-auto flex w-full max-w-[100rem] flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="numeral flex-1 text-2xl">Draw a route</h1>
        <Link to="/routes" className="inline-flex min-h-touch items-center px-2 text-dim hover:text-chalk">
          Back to routes
        </Link>
      </div>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1.7fr)_minmax(0,1fr)]">
        <div className="flex flex-col gap-2">
          <MapView
            data-testid="draw-map"
            className="h-96 lg:h-[34rem]"
            center={view.center}
            zoom={view.zoom}
            onReady={setMap}
            onClick={add}
          />
          <p className="text-sm text-dim">Click the map to add waypoints, up to {MAX_WAYPOINTS}. BRouter follows roads between them.</p>
        </div>

        <Card className="flex flex-col gap-4">
          <Select
            label="Routing profile"
            value={profile ?? ''}
            onChange={(e) => setProfileChoice((e.target.value || null) as Profile | null)}
          >
            <option value="">Server default</option>
            {PROFILES.map((p) => (
              <option key={p.value} value={p.value}>
                {p.label}
              </option>
            ))}
          </Select>

          <div className="flex flex-wrap items-center gap-2">
            <Button disabled={!map || full} onClick={() => map && add(map.getCenter())}>
              <Crosshair aria-hidden className="size-5" />
              Add waypoint at map centre
            </Button>
            <Button disabled={waypoints.length === 0} onClick={() => change((w) => w.slice(0, -1))}>
              <Undo2 aria-hidden className="size-5" />
              Undo
            </Button>
            <Button disabled={waypoints.length === 0} onClick={() => change(() => [])}>
              <Eraser aria-hidden className="size-5" />
              Clear
            </Button>
            <span className="numeral text-dim" aria-live="polite">
              {waypoints.length} / {MAX_WAYPOINTS} waypoints
            </span>
          </div>
          {full && <Banner tone="info">A route can have at most {MAX_WAYPOINTS} waypoints.</Banner>}

          {waypoints.length > 0 && (
            <ol className="flex max-h-56 flex-col gap-1 overflow-y-auto">
              {waypoints.map((w, i) => (
                <li key={i} className="flex items-center gap-2">
                  <span className="numeral w-7 text-right text-dim">{i + 1}</span>
                  <span className="min-w-0 flex-1 truncate text-sm">
                    {w[1].toFixed(4)}, {w[0].toFixed(4)}
                  </span>
                  <Button aria-label={`Remove waypoint ${i + 1}`} onClick={() => change((ws) => ws.filter((_, j) => j !== i))}>
                    <Trash2 aria-hidden className="size-5" />
                  </Button>
                </li>
              ))}
            </ol>
          )}

          {fail &&
            (fail.missing.length > 0 ? (
              <>
                <MissingTiles key={fail.missing.join()} tiles={fail.missing} admin={isAdmin} />
                <div>
                  <Button onClick={() => setRetry((n) => n + 1)}>Try again</Button>
                </div>
              </>
            ) : (
              <Banner tone="error">{fail.message}</Banner>
            ))}

          {waypoints.length >= 2 && !fail && busy && !preview && (
            <p role="status" className="text-dim">
              Routing…
            </p>
          )}
        </Card>
      </div>

      {preview && (
        <Card className="flex flex-col gap-4" aria-busy={busy}>
          <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
            <RouteStats route={preview.route} units={units} />
            <ProfileCanvas units={units} profile={preview.profile} height={200} />
          </div>
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              setFail(null);
              save.mutate();
            }}
          >
            <div className="min-w-48 flex-1">
              <Field
                label="Route name"
                value={name}
                maxLength={100}
                placeholder="Route"
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <Button type="submit" variant="primary" disabled={save.isPending || busy || stale}>
              {save.isPending ? 'Saving…' : 'Save route'}
            </Button>
          </form>
        </Card>
      )}
    </div>
  );
}
