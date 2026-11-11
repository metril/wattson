import type { components } from '@/lib/openapi';
import { Badge } from '@/components/Badge';
import { CountUp } from '@/components/CountUp';
import { SectionHeader } from '@/components/SectionHeader';
import { StatTile, type MetricColor } from '@/components/StatTile';
import { distUnit, elevUnit, fixed0, hms, speedUnit, toDist, toElev, toSpeed, type Units } from '@/lib/format';

type Ride = components['schemas']['RideOut'];
type Row = { label: string; value: string; unit?: string; color?: MetricColor };

const has = (v: number | null | undefined): v is number => v != null;

/** The hero summary: duration, distance, average and normalized power, TSS, with IF and VI badges. */
export function RideHero({ ride, units = 'metric' }: { ride: Ride; units?: Units }) {
  return (
    <section aria-label="Summary" className="flex flex-col gap-4">
      <dl className="grid grid-cols-2 gap-x-6 gap-y-5 sm:grid-cols-3 lg:grid-cols-5">
        <StatTile size="lg" label="Duration" value={hms(ride.duration_s)} />
        <StatTile size="lg" label="Distance" value={toDist(ride.distance_m, units).toFixed(2)} unit={distUnit(units)} color="speed" />
        <StatTile size="lg" label="Avg power" value={<CountUp value={ride.avg_power_w} />} unit="W" color="power" />
        <StatTile size="lg" label="NP" value={<CountUp value={ride.np_w} />} unit="W" color="power" />
        {has(ride.tss) && <StatTile size="lg" label="TSS" value={<CountUp value={ride.tss} />} />}
      </dl>
      {(has(ride.intensity_factor) || has(ride.variability_index)) && (
        <div className="flex flex-wrap gap-2">
          {has(ride.intensity_factor) && <Badge tone="accent">IF {ride.intensity_factor.toFixed(2)}</Badge>}
          {has(ride.variability_index) && <Badge>VI {ride.variability_index.toFixed(2)}</Badge>}
        </div>
      )}
    </section>
  );
}

function Group({ title, rows }: { title: string; rows: (Row | false)[] }) {
  const shown = rows.filter((r): r is Row => !!r);
  if (shown.length === 0) return null;
  return (
    <section aria-label={title} className="rounded-panel border border-line-soft bg-panel p-4 shadow-panel">
      <SectionHeader title={title} as="h3" />
      <dl className="grid grid-cols-2 gap-x-4 gap-y-4">
        {shown.map((r) => (
          <StatTile key={r.label} label={r.label} value={r.value} unit={r.unit} color={r.color} />
        ))}
      </dl>
    </section>
  );
}

/** Best average power over 5 s to 60 min as a row of bars, tallest = the ride's peak. */
function Bests({ ride }: { ride: Ride }) {
  const items = [
    { label: '5 s', v: ride.peak_5s_w },
    { label: '1 min', v: ride.peak_1m_w },
    { label: '5 min', v: ride.peak_5m_w },
    { label: '20 min', v: ride.peak_20m_w },
    { label: '60 min', v: ride.peak_60m_w },
  ].filter((x): x is { label: string; v: number } => has(x.v) && x.v > 0);
  if (items.length === 0) return null;
  const top = Math.max(...items.map((x) => x.v));
  return (
    <section aria-label="Power bests" className="rounded-panel border border-line-soft bg-panel p-4 shadow-panel">
      <SectionHeader title="Power bests" as="h3" />
      <ul className="flex h-32 items-end gap-2">
        {items.map((x) => (
          <li key={x.label} className="flex h-full min-w-0 flex-1 flex-col items-center justify-end gap-1">
            <span className="numeral text-sm text-m-power">{fixed0(x.v)}</span>
            <span
              className="w-full rounded-t-md bg-m-power/70"
              style={{ height: `${Math.max(6, (x.v / top) * 100) * 0.62}%` }}
              aria-hidden
            />
            <span className="text-xs text-dim">
              <span className="sr-only">Best </span>
              {x.label}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Grouped detail figures of a finished ride; figures without a value are left out. Groups fill the row so none is left alone. */
export function RideStats({ ride, units = 'metric' }: { ride: Ride; units?: Units }) {
  return (
    <div className="grid gap-4" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 15rem), 1fr))' }}>
      <Group
        title="Effort"
        rows={[
          { label: 'Energy', value: fixed0(ride.kj), unit: 'kJ' },
          ride.max_power_w > 0 && { label: 'Max power', value: fixed0(ride.max_power_w), unit: 'W', color: 'power' },
          has(ride.efficiency_factor) && { label: 'Efficiency (EF)', value: ride.efficiency_factor.toFixed(2) },
          has(ride.decoupling_pct) && { label: 'Decoupling', value: ride.decoupling_pct.toFixed(1), unit: '%' },
        ]}
      />
      <Bests ride={ride} />
      <Group
        title="Cadence and heart rate"
        rows={[
          ride.avg_cadence_rpm > 0 && { label: 'Avg cadence', value: fixed0(ride.avg_cadence_rpm), unit: 'rpm', color: 'cadence' },
          ride.max_cadence_rpm > 0 && { label: 'Max cadence', value: fixed0(ride.max_cadence_rpm), unit: 'rpm', color: 'cadence' },
          ride.avg_hr_bpm > 0 && { label: 'Avg HR', value: fixed0(ride.avg_hr_bpm), unit: 'bpm', color: 'hr' },
          ride.max_hr_bpm > 0 && { label: 'Max HR', value: fixed0(ride.max_hr_bpm), unit: 'bpm', color: 'hr' },
        ]}
      />
      <Group
        title="Terrain and speed"
        rows={[
          ride.ascent_m > 0 && { label: 'Ascent', value: toElev(ride.ascent_m, units).toFixed(0), unit: elevUnit(units), color: 'elev' },
          has(ride.vam_m_per_h) && { label: 'Climb rate', value: toElev(ride.vam_m_per_h, units).toFixed(0), unit: `${elevUnit(units)}/h`, color: 'elev' },
          ride.avg_speed_kmh > 0 && { label: 'Avg speed', value: toSpeed(ride.avg_speed_kmh, units).toFixed(1), unit: speedUnit(units), color: 'speed' },
        ]}
      />
    </div>
  );
}
