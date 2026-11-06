import type { components } from '@/lib/openapi';
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
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3 xl:grid-cols-5">
      {items.map(([label, value, unit]) => (
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
}
