import type { components } from '@/lib/openapi';
import { ZoneBars } from '@/features/history/ZoneBars';
import { distUnit, elevUnit, fixed0, hms, speedUnit, toDist, toElev, toSpeed, type Units } from '@/lib/format';

/** The headline figures of a finished ride. */
export function RideStats({ ride, units = 'metric' }: { ride: components['schemas']['RideOut']; units?: Units }) {
  const items: [string, string, string][] = [
    ['Duration', hms(ride.duration_s), ''],
    ['Distance', toDist(ride.distance_m, units).toFixed(2), distUnit(units)],
    ['Ascent', toElev(ride.ascent_m, units).toFixed(0), elevUnit(units)],
    ['Avg power', fixed0(ride.avg_power_w), 'W'],
    ['Max power', fixed0(ride.max_power_w), 'W'],
    ['NP', fixed0(ride.np_w), 'W'],
    ['Energy', fixed0(ride.kj), 'kJ'],
    ['Avg cadence', fixed0(ride.avg_cadence_rpm), 'rpm'],
    ['Avg HR', ride.avg_hr_bpm > 0 ? fixed0(ride.avg_hr_bpm) : '–', ride.avg_hr_bpm > 0 ? 'bpm' : ''],
    ['Avg speed', toSpeed(ride.avg_speed_kmh, units).toFixed(1), speedUnit(units)],
  ];
  const num = (v: number | null | undefined, digits: number, unit = ''): [string, string] =>
    v == null ? ['–', ''] : [v.toFixed(digits), unit];
  const watts = (v: number | null | undefined): [string, string] => (v == null ? ['–', ''] : [fixed0(v), 'W']);
  const metrics: [string, [string, string]][] = [
    ['TSS', num(ride.tss, 0)],
    ['Intensity (IF)', num(ride.intensity_factor, 2)],
    ['Variability (VI)', num(ride.variability_index, 2)],
    ['Efficiency (EF)', num(ride.efficiency_factor, 2)],
    ['Decoupling', num(ride.decoupling_pct, 1, '%')],
    ['Climb rate', ride.vam_m_per_h == null ? ['–', ''] : [toElev(ride.vam_m_per_h, units).toFixed(0), `${elevUnit(units)}/h`]],
    ['Max cadence', ride.max_cadence_rpm > 0 ? [fixed0(ride.max_cadence_rpm), 'rpm'] : ['–', '']],
    ['Best 5 s', watts(ride.peak_5s_w)],
    ['Best 1 min', watts(ride.peak_1m_w)],
    ['Best 5 min', watts(ride.peak_5m_w)],
    ['Best 20 min', watts(ride.peak_20m_w)],
    ['Best 60 min', watts(ride.peak_60m_w)],
  ];
  const grid = (rows: [string, string, string][]) => (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3 xl:grid-cols-5">
      {rows.map(([label, value, unit]) => (
        <div key={label}>
          <dt className="text-sm text-dim">{label}</dt>
          <dd className="numeral text-3xl">
            {value}
            {unit && <span className="ml-1 text-base font-medium text-dim">{unit}</span>}
          </dd>
        </div>
      ))}
    </dl>
  );
  return (
    <div className="flex flex-col gap-6">
      {grid(items)}
      <section aria-label="Training metrics" className="flex flex-col gap-4 border-t border-line pt-4">
        {grid(metrics.map(([l, [v, u]]) => [l, v, u]))}
      </section>
      <ZoneBars title="Time in power zones" kind="power" seconds={ride.power_zone_s} />
      <ZoneBars title="Time in heart-rate zones" kind="hr" seconds={ride.hr_zone_s} />
    </div>
  );
}
