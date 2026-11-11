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
import { RideHero, RideStats } from '@/features/history/RideStats';
import { ZonesCard } from '@/features/history/ZonesCard';
import { SectionHeader } from '@/components/SectionHeader';
import { PageHeader } from '@/components/PageHeader';
import { Reveal } from '@/components/Reveal';
import { RIDERS_KEY } from '@/pages/Riders';
import { zoneBounds } from '@/lib/zones';
import { SeriesChart, type Band } from '@/features/history/SeriesChart';
import { invalidateRideData, RIDES_KEY } from '@/lib/rides';
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
  // The rider's current max HR sets the heart-rate zone ranges; the zones card copes without it.
  const riders = useQuery({
    queryKey: RIDERS_KEY,
    enabled: ride.isSuccess,
    queryFn: async () => unwrap(await api.GET('/api/riders')),
  });
  const maxHr = riders.data?.find((x) => x.id === riderId)?.max_hr_bpm ?? null;
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

  const [zoomed, setZoomed] = useState(false);
  const [reset, setReset] = useState(0);
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
      invalidateRideData(qc);
      navigate('/history', { replace: true });
    },
    onError: (e) => {
      setConfirm(false);
      setError(
        e instanceof ApiError && e.code === 'ride_in_progress' ? 'This ride is still being recorded.' : errMessage(e),
      );
    },
  });

  const rd = ride.data;
  const charts = useMemo(() => {
    if (!rd) return [];
    const sr = rd.series;
    const edges = rd.ftp_w ? [0, ...zoneBounds('power', rd.ftp_w)] : [];
    const powerBands: Band[] = edges.map((from, i) => ({ from, to: edges[i + 1] ?? null, color: `--zone-${i + 1}` }));
    const out: { label: string; unit: string; color: string; y: number[]; zero?: boolean; bands?: Band[] }[] = [
      { label: 'Power', unit: 'W', color: '--m-power', y: sr.power_w, bands: powerBands },
      ...(sr.hr_bpm.some((v) => v > 0) ? [{ label: 'Heart rate', unit: 'bpm', color: '--m-hr', y: sr.hr_bpm }] : []),
      { label: 'Cadence', unit: 'rpm', color: '--m-cadence', y: sr.cadence_rpm },
      { label: 'Speed', unit: speedUnit(units), color: '--m-speed', y: sr.speed_kmh.map((v) => toSpeed(v, units)) },
      ...(sr.ele_m && sr.ele_m.length > 0 ? [{ label: 'Elevation', unit: elevUnit(units), color: '--m-elev', zero: false, y: sr.ele_m.map((v) => toElev(v, units)) }] : []),
    ];
    return out;
  }, [rd, units]);

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
  return (
    <div className="mx-auto flex w-full max-w-[100rem] flex-col gap-4">
      <PageHeader
        title={r.route_name || 'Free ride'}
        subtitle={`${rideDate(r.started_at)} · ${r.rider_name}`}
        actions={
          <>
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
          </>
        }
      />

      {error && <Banner tone="error">{error}</Banner>}

      <Reveal>
        <Card>
          <RideHero ride={r} units={units} />
        </Card>
      </Reveal>

      <Reveal delay={0.06}>
        <RideStats ride={r} units={units} />
      </Reveal>

      <Reveal delay={0.12}>
        <div className={trace ? 'grid items-start gap-4 lg:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]' : 'grid gap-4'}>
          {trace && <MapView data-testid="ride-map" className="h-72 md:h-96 lg:h-[26rem]" onReady={setMap} />}
          <ZonesCard ride={r} maxHr={maxHr} stack={!!trace} />
        </div>
      </Reveal>

      {thisCurve && thisCurve.length > 0 && (
        <Reveal delay={0.18}>
          <Card>
            <PowerCurve points={bestPts && bestPts.length > 0 ? bestPts : thisCurve} ride={bestPts && bestPts.length > 0 ? thisCurve : undefined} rideLabel="this ride" label="Power curve" />
          </Card>
        </Reveal>
      )}

      <Reveal delay={0.24}>
        <Card className="grid gap-5">
          <div className="flex items-center justify-between gap-3">
            <SectionHeader title="Ride data" hint="Drag across a chart to zoom in; the others follow." />
            {zoomed && <Button variant="secondary" onClick={() => setReset((n) => n + 1)}>Reset zoom</Button>}
          </div>
          {charts.map((c) => (
            <SeriesChart key={c.label} x={xs} xUnit={distUnit(units)} y={c.y} label={c.label} unit={c.unit} color={c.color} zero={c.zero} bands={c.bands} group="ride" resetToken={reset} onZoom={setZoomed} />
          ))}
        </Card>
      </Reveal>
    </div>
  );
}
