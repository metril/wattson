import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Download, X } from 'lucide-react';
import { api, ApiError, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { tileGrid } from '@/lib/tiles';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { Field } from '@/components/Field';
import MapView, { type MapApi } from '@/features/map/MapView';
import { tok } from '@/features/routes/canvas';
import { useSettings } from './form';

type Tiles = components['schemas']['TilesOut'];
type Tile = components['schemas']['TileOut'];

export const TILES_KEY = ['maps', 'tiles'] as const;
const FAST_MS = 1000;
const SLOW_MS = 30_000;

const busy = (t: Tile) => t.status === 'queued' || t.status === 'downloading';
const GRID = tileGrid();
const NAMES = new Set(GRID.map((g) => g.name));

const mb = (b: number) => `${(b / 1e6).toFixed(b >= 1e8 ? 0 : 1)} MB`;
const progress = (t: Tile) => (t.status === 'downloading' && t.total_bytes > 0 ? Math.min(1, t.bytes / t.total_bytes) : 0);

function rectangle([w, s, e, n]: [number, number, number, number]): GeoJSON.Polygon {
  return {
    type: 'Polygon',
    coordinates: [
      [
        [w, s],
        [e, s],
        [e, n],
        [w, n],
        [w, s],
      ],
    ],
  };
}

function statusText(t: Tile): string {
  switch (t.status) {
    case 'present':
      return `Downloaded, ${mb(t.bytes)}`;
    case 'queued':
      return 'Queued';
    case 'downloading':
      return t.total_bytes > 0 ? `Downloading ${Math.round(progress(t) * 100)} %` : `Downloading ${mb(t.bytes)}`;
    case 'error':
      return `Failed${t.error ? `: ${t.error}` : ''}`;
  }
}

/** Two-tap delete: the first tap arms the button for 4 s. */
function DeleteButton({ name, pending, onConfirm }: { name: string; pending: boolean; onConfirm(): void }) {
  const [armed, setArmed] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => () => clearTimeout(timer.current), []);
  return (
    <Button
      variant={armed ? 'danger' : 'secondary'}
      disabled={pending}
      aria-label={armed ? `Confirm delete ${name}` : `Delete ${name}`}
      onClick={() => {
        if (armed) {
          clearTimeout(timer.current);
          setArmed(false);
          onConfirm();
        } else {
          setArmed(true);
          timer.current = setTimeout(() => setArmed(false), 4000);
        }
      }}
    >
      {armed ? 'Tap again to delete' : 'Delete'}
    </Button>
  );
}

function Manager({ data }: { data: Tiles }) {
  const qc = useQueryClient();
  const settings = useSettings();
  const [map, setMap] = useState<MapApi | null>(null);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [focus, setFocus] = useState<string | null>(null);
  const [typed, setTyped] = useState('');
  const [typedError, setTypedError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const byName = useMemo(() => new Map(data.tiles.map((t) => [t.name, t])), [data.tiles]);
  // An unreadable or empty index means "anything goes": names can still be downloaded directly.
  const offered = useMemo(() => (data.available.length > 0 ? new Set(data.available) : null), [data.available]);
  const selectable = useCallback(
    (name: string) => {
      const t = byName.get(name);
      if (t && t.status !== 'error') return false;
      return offered ? offered.has(name) : true;
    },
    [byName, offered],
  );

  const toggle = useCallback(
    (name: string) => {
      setFocus(byName.has(name) ? name : null);
      if (!selectable(name)) return;
      setSelected((p) => {
        const n = new Set(p);
        if (!n.delete(name)) n.add(name);
        return n;
      });
    },
    [byName, selectable],
  );

  useEffect(() => {
    if (!map) return;
    const ok = tok('--ok', '#6fd36a');
    const warn = tok('--warn', '#f1c232');
    const danger = tok('--danger', '#ff6b63');
    const accent = tok('--accent', '#54a9ff');
    const grey = tok('--faint', '#6b7c82');
    const features: GeoJSON.Feature[] = GRID.map((g) => {
      const t = byName.get(g.name);
      let status: string;
      let color = accent;
      let opacity = 0.03;
      if (selected.has(g.name)) {
        status = 'selected';
        opacity = 0.6;
      } else if (t) {
        status = t.status;
        color = t.status === 'present' ? ok : t.status === 'error' ? danger : warn;
        opacity = 0.5;
      } else if (offered && !offered.has(g.name)) {
        status = 'unavailable';
        color = grey;
        opacity = 0.25;
      } else {
        status = 'available';
      }
      return {
        type: 'Feature',
        properties: { name: g.name, status, progress: t ? progress(t) : 0, color, opacity },
        geometry: rectangle(g.bbox),
      };
    });
    map.setTiles({ type: 'FeatureCollection', features }, toggle);
  }, [map, byName, offered, selected, toggle]);

  const refresh = () => qc.invalidateQueries({ queryKey: TILES_KEY });

  const download = useMutation({
    mutationFn: async (names: string[]) => {
      const res = await Promise.allSettled(
        names.map(async (name) => unwrap(await api.POST('/api/maps/tiles/{name}', { params: { path: { name } } }))),
      );
      const failed = res.flatMap((r, i) => (r.status === 'rejected' ? [`${names[i]}: ${errMessage(r.reason)}`] : []));
      return { failed, ok: names.filter((_, i) => res[i].status === 'fulfilled') };
    },
    onSuccess: ({ failed, ok }) => {
      setSelected((p) => new Set([...p].filter((n) => !ok.includes(n))));
      setError(failed.length ? failed.join('; ') : null);
      void refresh();
    },
    onError: (e) => setError(errMessage(e)),
  });
  const remove = useMutation({
    mutationFn: async (name: string) =>
      unwrap(await api.DELETE('/api/maps/tiles/{name}', { params: { path: { name } } })),
    onSuccess: () => {
      setError(null);
      void refresh();
    },
    onError: (e) => setError(errMessage(e)),
  });

  function addTyped() {
    const name = typed.trim().toUpperCase();
    if (!NAMES.has(name)) {
      setTypedError('Use a region name such as E5_N45 (multiples of 5, west/east then south/north).');
      return;
    }
    if (!selectable(name)) {
      setTypedError(byName.has(name) ? `${name} is already downloaded or queued.` : `${name} is not offered by the segments server.`);
      return;
    }
    setTypedError(null);
    setTyped('');
    setSelected((p) => new Set(p).add(name));
  }

  const names = [...selected].sort();
  const dir = data.dir;
  const source = settings.data?.brouter.segments_url ?? 'the segments server';

  return (
    <div className="flex flex-col gap-4">
      {!dir.exists && (
        <Banner tone="error">
          Segments folder <code>{dir.path}</code> not found. Mount the brouter-segments volume at this path (see compose.yml).
        </Banner>
      )}
      {dir.exists && !dir.writable && <Banner tone="error">Segments folder is read-only.</Banner>}
      {data.available_error && (
        <Banner tone="warn">
          Could not load the region list from {source}; you can still download by name.
        </Banner>
      )}
      {error && <Banner tone="error">{error}</Banner>}

      <p className="text-sm text-dim">
        Each region is 5°×5° (up to ~300 MB). BRouter picks up new regions without a restart.
      </p>

      <MapView data-testid="regions-map" className="h-80 md:h-[26rem]" center={[10, 30]} zoom={1.6} onReady={setMap} />
      <ul className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-dim" aria-label="Map legend">
        <li>Green: downloaded</li>
        <li>Amber: queued or downloading</li>
        <li>Red: failed</li>
        <li>Blue: selected</li>
        <li>Grey: not offered</li>
      </ul>

      <Card className="flex flex-col gap-3">
        <h2 className="numeral text-xl">Download</h2>
        <p className="text-sm text-dim">Click regions on the map, or type a name.</p>
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            addTyped();
          }}
        >
          <div className="min-w-40 flex-1">
            <Field
              label="Region name"
              value={typed}
              placeholder="E5_N45"
              autoComplete="off"
              error={typedError ?? undefined}
              onChange={(e) => setTyped(e.target.value)}
            />
          </div>
          <Button type="submit">Add</Button>
        </form>
        {names.length > 0 && (
          <ul className="flex flex-wrap gap-2" aria-label="Selected regions">
            {names.map((n) => (
              <li key={n}>
                <Button aria-label={`Unselect ${n}`} onClick={() => toggle(n)}>
                  {n}
                  <X aria-hidden className="size-4" />
                </Button>
              </li>
            ))}
          </ul>
        )}
        <div>
          <Button
            variant="primary"
            disabled={names.length === 0 || download.isPending || !dir.exists || !dir.writable}
            onClick={() => download.mutate(names)}
          >
            <Download aria-hidden className="size-5" />
            {`Download ${names.length} ${names.length === 1 ? 'region' : 'regions'}`}
          </Button>
        </div>
      </Card>

      <Card className="flex flex-col gap-3">
        <h2 className="numeral text-xl">Regions on this server</h2>
        {data.tiles.length === 0 ? (
          <p className="text-dim">No regions yet.</p>
        ) : (
          <ul className="flex flex-col divide-y divide-line" aria-label="Regions">
            {data.tiles.map((t) => (
              <li
                key={t.name}
                aria-current={focus === t.name ? 'true' : undefined}
                className={`flex flex-wrap items-center gap-3 py-1 ${focus === t.name ? 'bg-raised' : ''}`}
              >
                <span className="numeral min-w-24 text-lg">{t.name}</span>
                <span className="min-w-0 flex-1 text-sm text-dim">{statusText(t)}</span>
                {busy(t) && t.status === 'downloading' && t.total_bytes > 0 && (
                  <progress
                    className="w-24"
                    max={100}
                    value={Math.round(progress(t) * 100)}
                    aria-label={`${t.name} download progress`}
                  />
                )}
                {busy(t) ? (
                  <Button disabled={remove.isPending} aria-label={`Cancel ${t.name}`} onClick={() => remove.mutate(t.name)}>
                    Cancel
                  </Button>
                ) : (
                  <DeleteButton name={t.name} pending={remove.isPending} onConfirm={() => remove.mutate(t.name)} />
                )}
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}

export function MapRegionsPage() {
  const q = useQuery<Tiles>({
    queryKey: TILES_KEY,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/maps/tiles')),
    // Fast while anything is in flight, slow otherwise; stops with the page.
    refetchInterval: (query) => (query.state.data?.tiles.some(busy) ? FAST_MS : SLOW_MS),
  });

  const noLocal = q.error instanceof ApiError && q.error.code === 'no_local_regions';
  return (
    <div className="flex flex-col gap-4">
      <h2 className="numeral text-2xl">Map regions</h2>
      {q.data && !noLocal ? (
        <>
          {q.isError && !noLocal && <Banner tone="error">{errMessage(q.error)}</Banner>}
          <Manager data={q.data} />
        </>
      ) : q.isPending ? (
        <p role="status" className="text-dim">
          Loading…
        </p>
      ) : noLocal ? (
        <Banner tone="info">
          Using a remote BRouter — local map regions disabled. To manage regions here, set a segments folder in{' '}
          <Link to="/settings/integrations" className="text-accent underline">
            Integrations
          </Link>
          .
        </Banner>
      ) : (
        <Banner tone="error">{errMessage(q.error)}</Banner>
      )}
    </div>
  );
}
