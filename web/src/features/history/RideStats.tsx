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
    <section aria-label={title}>
      <SectionHeader title={title} as="h3" />
      <dl className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3">
        {shown.map((r) => (
          <StatTile key={r.label} label={r.label} value={r.value} unit={r.unit} color={r.color} />
        ))}
      </dl>
    </section>
  );
}

/** Grouped detail figures of a finished ride; figures without a value are left out. */
export function RideStats({ ride, units = 'metric' }: { ride: Ride; units?: Units }) {
  const w = (v: number | null | undefined, label: string): Row | false => has(v) && { label, value: fixed0(v), unit: 'W', color: 'power' };
  return (
    <div className="flex flex-col gap-6">
      <Group
        title="Effort"
        rows={[
          { label: 'Energy', value: fixed0(ride.kj), unit: 'kJ' },
          ride.max_power_w > 0 && { label: 'Max power', value: fixed0(ride.max_power_w), unit: 'W', color: 'power' },
          has(ride.efficiency_factor) && { label: 'Efficiency (EF)', value: ride.efficiency_factor.toFixed(2) },
          has(ride.decoupling_pct) && { label: 'Decoupling', value: ride.decoupling_pct.toFixed(1), unit: '%' },
        ]}
      />
      <Group
        title="Power bests"
        rows={[
          w(ride.peak_5s_w, 'Best 5 s'),
          w(ride.peak_1m_w, 'Best 1 min'),
          w(ride.peak_5m_w, 'Best 5 min'),
          w(ride.peak_20m_w, 'Best 20 min'),
          w(ride.peak_60m_w, 'Best 60 min'),
        ]}
      />
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
