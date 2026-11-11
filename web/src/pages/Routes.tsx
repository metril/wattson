import { useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { PenLine, SlidersHorizontal, Upload } from 'lucide-react';
import { api, ApiError, unwrap } from '@/lib/api';
import { useUnits } from '@/lib/auth';
import { errMessage } from '@/lib/errors';
import { fmtDist, fmtElev, rideDate } from '@/lib/format';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { PageHeader } from '@/components/PageHeader';
import { ROUTES_KEY } from '@/features/routes/queries';
import { sourceLabel } from '@/features/routes/RouteStats';

type Route = components['schemas']['RouteOut'];

type SortKey = 'name' | 'distance' | 'ascent' | 'source' | 'created';
type SortDir = 'asc' | 'desc';
type Sort = { key: SortKey; dir: SortDir };

const SORT_KEY = 'wattson.routes.sort';
const DEFAULT_SORT: Sort = { key: 'created', dir: 'desc' };
const KEYS: SortKey[] = ['name', 'distance', 'ascent', 'source', 'created'];
const firstDir = (k: SortKey): SortDir => (k === 'name' || k === 'source' ? 'asc' : 'desc');

function loadSort(): Sort {
  try {
    const v = JSON.parse(localStorage.getItem(SORT_KEY) ?? 'null') as Sort | null;
    if (v && KEYS.includes(v.key) && (v.dir === 'asc' || v.dir === 'desc')) return { key: v.key, dir: v.dir };
  } catch {
    /* unreadable or unavailable storage: use the default */
  }
  return DEFAULT_SORT;
}

const collator = (a: string, b: string) => a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' });

function compare(a: Route, b: Route, key: SortKey): number {
  switch (key) {
    case 'name':
      return collator(a.name, b.name);
    case 'source':
      return collator(sourceLabel(a.source), sourceLabel(b.source));
    case 'distance':
      return a.distance_m - b.distance_m;
    case 'ascent':
      return a.ascent_m - b.ascent_m;
    case 'created':
      return Date.parse(a.created_at) - Date.parse(b.created_at) || 0;
  }
}

const btn =
  'inline-flex min-h-touch items-center justify-center gap-2 rounded-control px-4 font-medium';

/** User-facing text for a failed import. */
export function importMessage(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.code === 'unsupported_format') return 'Unsupported file. Use GPX, TCX or FIT.';
    if (e.code === 'too_large' || e.status === 413) return 'File is larger than 20 MB.';
    if (e.code === 'no_elevation') return e.message;
  }
  return errMessage(e);
}

export function RoutesPage() {
  const units = useUnits();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const q = useQuery<Route[]>({
    queryKey: ROUTES_KEY,
    queryFn: async () => unwrap(await api.GET('/api/routes')),
  });
  const [error, setError] = useState<string | null>(null);

  const upload = useMutation({
    mutationFn: async (file: File) => {
      const fd = new FormData();
      fd.append('file', file);
      return unwrap(
        await api.POST('/api/routes/import', {
          // The browser sets the multipart boundary; the body goes through untouched.
          body: fd as unknown as { file: string },
          bodySerializer: (b) => b as unknown as FormData,
        }),
      );
    },
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: ROUTES_KEY });
      navigate(`/routes/${r.id}`);
    },
    onError: (e) => setError(importMessage(e)),
  });

  const [sort, setSort] = useState<Sort>(loadSort);
  const routes = useMemo(() => {
    const sign = sort.dir === 'asc' ? 1 : -1;
    return [...(q.data ?? [])].sort((a, b) => sign * compare(a, b, sort.key) || collator(a.name, b.name));
  }, [q.data, sort]);

  const toggleSort = (key: SortKey) => {
    const next: Sort =
      sort.key === key ? { key, dir: sort.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: firstDir(key) };
    setSort(next);
    try {
      localStorage.setItem(SORT_KEY, JSON.stringify(next));
    } catch {
      /* storage unavailable: sort still applies for this visit */
    }
  };

  const th = (key: SortKey, label: string, right = false) => (
    <th
      scope="col"
      aria-sort={sort.key === key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'}
      className={`px-4 py-3 font-medium ${right ? 'text-right' : ''}`}
    >
      <button
        type="button"
        onClick={() => toggleSort(key)}
        className="inline-flex min-h-touch items-center gap-1 font-medium hover:text-chalk"
      >
        {label}
        <span aria-hidden className="w-3 text-xs">
          {sort.key === key ? (sort.dir === 'asc' ? '▲' : '▼') : ''}
        </span>
      </button>
    </th>
  );

  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-5">
      <PageHeader title="Routes" subtitle="Rides on real roads and synthetic climbs" actions={<>
        <label
          className={`${btn} cursor-pointer bg-accent text-accent-ink hover:brightness-110 has-[:focus-visible]:outline-3 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-accent`}
        >
          <Upload aria-hidden className="size-5" />
          {upload.isPending ? 'Importing…' : 'Import file'}
          <input
            type="file"
            accept=".gpx,.tcx,.fit"
            className="sr-only"
            aria-label="Route file (GPX, TCX or FIT)"
            disabled={upload.isPending}
            onChange={(e) => {
              const f = e.target.files?.[0];
              e.target.value = '';
              if (!f) return;
              setError(null);
              upload.mutate(f);
            }}
          />
        </label>
        <Link to="/routes/new/draw" data-nav className={`${btn} border border-line bg-raised hover:border-accent`}>
          <PenLine aria-hidden className="size-5" />
          Draw
        </Link>
        <Link to="/routes/new/synthetic" data-nav className={`${btn} border border-line bg-raised hover:border-accent`}>
          <SlidersHorizontal aria-hidden className="size-5" />
          Synthetic
        </Link>
      </>} />

      {error && <Banner tone="error">{error}</Banner>}

      {q.isPending ? (
        <p role="status" className="text-dim">
          Loading…
        </p>
      ) : q.isError ? (
        <Banner tone="error">{errMessage(q.error)}</Banner>
      ) : routes.length === 0 ? (
        <div className="rounded-panel border border-dashed border-line p-8 text-center text-dim">
          <p className="text-lg text-chalk">No routes yet</p>
          <p>Import a GPX, TCX or FIT file, draw a route on the map, or build a synthetic climb.</p>
        </div>
      ) : (
        <div className="overflow-x-auto rounded-panel border border-line bg-panel">
          <table className="w-full text-left">
            <thead className="text-sm text-dim">
              <tr className="border-b border-line">
                {th('name', 'Name')}
                {th('distance', 'Distance', true)}
                {th('ascent', 'Ascent', true)}
                {th('source', 'Source')}
                {th('created', 'Created')}
              </tr>
            </thead>
            <tbody>
              {routes.map((r) => (
                <tr key={r.id} className="border-b border-line last:border-0 hover:bg-raised">
                  <td className="p-0">
                    <Link
                      to={`/routes/${r.id}`}
                      data-nav
                      className="flex min-h-14 items-center px-4 text-lg font-medium text-chalk hover:text-accent"
                    >
                      {r.name}
                    </Link>
                  </td>
                  <td className="numeral whitespace-nowrap px-4 text-right text-lg">{fmtDist(r.distance_m, units)}</td>
                  <td className="numeral whitespace-nowrap px-4 text-right text-lg">{fmtElev(r.ascent_m, units)}</td>
                  <td className="px-4">
                    <span className="rounded-full border border-line px-2.5 py-0.5 text-xs text-dim">
                      {sourceLabel(r.source)}
                    </span>
                  </td>
                  <td className="whitespace-nowrap px-4 text-dim">{rideDate(r.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
