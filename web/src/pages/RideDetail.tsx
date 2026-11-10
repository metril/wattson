import { useUnits } from '@/lib/auth';
import { distUnit, elevUnit, speedUnit, toDist, toElev, toSpeed } from '@/lib/format';
import { useEffect, useMemo, useRef, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router';
import { useMutation, useQuery, useQueryClient, type InfiniteData } from '@tanstack/react-query';
import type { components } from '@/lib/openapi';
import { api, ApiError, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { rideDate } from '@/lib/format';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import MapView, { type MapApi } from '@/features/map/MapView';
import { FitButton } from '@/features/history/FitButton';
import { StravaUploadButton } from '@/features/strava/StravaUploadButton';
import { RideStats } from '@/features/history/RideStats';
import { SeriesChart } from '@/features/history/SeriesChart';
import { RIDES_KEY } from '@/lib/rides';
import { PowerCurve } from '@/features/stats/PowerCurve';
import { curveKey } from '@/features/stats/queries';

const rideKey = (id: number) => [...RIDES_KEY, 'detail', id] as const;

function Gone() {
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3">
      <Banner tone="warn">This ride no longer exists. It may have been deleted.</Banner>
      <Link to="/history" className="inline-flex min-h-touch items-center px-1 text-accent">
        Back to history
      </Link>
    </div>
  );
}

export function RideDetailPage() {
  const id = Number(useParams().id);
  const valid = Number.isInteger(id) && id > 0;
  const qc = useQueryClient();
  const units = useUnits();
  const navigate = useNavigate();

  const ride = useQuery({
    queryKey: rideKey(id),
    enabled: valid,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/rides/{id}', { params: { path: { id }, query: { points: 600 } } })),
  });

  // This ride's power curve against the rider's all-time best.
  const riderId = ride.data?.rider_id ?? 0;
  const rideCurve = useQuery({
    queryKey: [...rideKey(id), 'curve'],
    enabled: ride.isSuccess,
    queryFn: async () => unwrap(await api.GET('/api/rides/{id}/power-curve', { params: { path: { id } } })),
  });
  const bestCurve = useQuery({
    queryKey: curveKey(riderId, 'all'),
    enabled: ride.isSuccess && riderId > 0,
    queryFn: async () => unwrap(await api.GET('/api/stats/power-curve', { params: { query: { window: 'all', rider_id: riderId } } })),
  });
  const thisCurve = rideCurve.data?.points;
  const bestPts = bestCurve.data?.points;

  const s = ride.data?.series;
  const xs = useMemo(() => (s ? s.distance_m.map((d) => toDist(d, units)) : []), [s, units]);
  const trace = useMemo(() => {
    if (!s?.lat || !s.lon || s.lat.length === 0) return null;
    const coordinates = s.lon.map((lo, i) => [lo, s.lat![i]]);
    let w = Infinity, south = Infinity, e = -Infinity, n = -Infinity;
    for (const [lo, la] of coordinates) {
      if (lo < w) w = lo;
      if (lo > e) e = lo;
      if (la < south) south = la;
      if (la > n) n = la;
    }
    return { line: { type: 'LineString' as const, coordinates }, bbox: [w, south, e, n] as [number, number, number, number] };
  }, [s]);

  const [map, setMap] = useState<MapApi | null>(null);
  useEffect(() => {
    if (!map || !trace) return;
    map.setLine(trace.line);
    map.fit(trace.bbox);
  }, [map, trace]);

  const [error, setError] = useState<string | null>(null);
  const [confirm, setConfirm] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => () => clearTimeout(timer.current), []);

  const del = useMutation({
    mutationFn: async () => unwrap(await api.DELETE('/api/rides/{id}', { params: { path: { id } } })),
    onSuccess: () => {
      qc.removeQueries({ queryKey: rideKey(id) });
      // Drop the ride from the cached list pages so the list never flashes it again.
      qc.setQueriesData<InfiniteData<components['schemas']['RideList']>>(
        { queryKey: RIDES_KEY, predicate: (q) => q.queryKey[1] !== 'detail' },
        (old) => old && { ...old, pages: old.pages.map((p) => ({ ...p, rides: p.rides.filter((x) => x.id !== id) })) },
      );
      void qc.invalidateQueries({ queryKey: RIDES_KEY });
      navigate('/history', { replace: true });
    },
    onError: (e) => {
      setConfirm(false);
      setError(
        e instanceof ApiError && e.code === 'ride_in_progress' ? 'This ride is still being recorded.' : errMessage(e),
      );
    },
  });

  if (!valid) return <Gone />;
  if (ride.isPending) return <p role="status" className="text-dim">Loading…</p>;
  if (ride.isError) {
    const e = ride.error;
    if (e instanceof ApiError && e.status === 404) return <Gone />;
    return (
      <Banner tone="error">
        {e instanceof ApiError && e.code === 'ride_in_progress' ? 'This ride is still being recorded.' : errMessage(e)}
      </Banner>
    );
  }
  const r = ride.data;
  const sr = r.series;
  const charts: { label: string; unit: string; color: string; y: number[] }[] = [
    { label: 'Power', unit: 'W', color: '--accent', y: sr.power_w },
    ...(sr.hr_bpm.some((v) => v > 0) ? [{ label: 'Heart rate', unit: 'bpm', color: '--danger', y: sr.hr_bpm }] : []),
    { label: 'Cadence', unit: 'rpm', color: '--ok', y: sr.cadence_rpm },
    { label: 'Speed', unit: speedUnit(units), color: '--warn', y: sr.speed_kmh.map((v) => toSpeed(v, units)) },
    ...(sr.ele_m && sr.ele_m.length > 0 ? [{ label: 'Elevation', unit: elevUnit(units), color: '--chalk', y: sr.ele_m.map((v) => toElev(v, units)) }] : []),
  ];

  return (
    <div className="mx-auto flex w-full max-w-[100rem] flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <div className="min-w-0 flex-1 basis-60">
          <h1 className="numeral truncate text-2xl">{r.route_name || 'Free ride'}</h1>
          <p className="text-sm text-dim">
            {rideDate(r.started_at)} · {r.rider_name}
          </p>
        </div>
        <FitButton rideId={r.id} />
        <StravaUploadButton key={r.id} rideId={r.id} initial={r.strava} />
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
      </div>

      {error && <Banner tone="error">{error}</Banner>}

      <div className={trace ? 'grid gap-4 lg:grid-cols-[minmax(0,1.7fr)_minmax(0,1fr)]' : 'grid gap-4'}>
        {trace && <MapView data-testid="ride-map" className="h-72 md:h-96 lg:h-[26rem]" onReady={setMap} />}
        <Card className="flex flex-col justify-between gap-5">
          <RideStats ride={r} units={units} />
        </Card>
      </div>

      {thisCurve && thisCurve.length > 0 && (
        <Card>
          <PowerCurve points={bestPts && bestPts.length > 0 ? bestPts : thisCurve} ride={bestPts && bestPts.length > 0 ? thisCurve : undefined} rideLabel="this ride" label="Power curve" />
        </Card>
      )}

      <Card className="grid gap-5 xl:grid-cols-2">
        {charts.map((c) => (
          <SeriesChart key={c.label} x={xs} xUnit={distUnit(units)} y={c.y} label={c.label} unit={c.unit} color={c.color} />
        ))}
      </Card>
    </div>
  );
}
