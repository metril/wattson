import type { components } from '@/lib/openapi';
import { fixed0, hms, km } from '@/lib/format';

/** The headline figures of a finished ride. */
export function RideStats({ ride }: { ride: components['schemas']['RideOut'] }) {
  const items: [string, string, string][] = [
    ['Duration', hms(ride.duration_s), ''],
    ['Distance', km(ride.distance_m), 'km'],
    ['Ascent', fixed0(ride.ascent_m), 'm'],
    ['Avg power', fixed0(ride.avg_power_w), 'W'],
    ['Max power', fixed0(ride.max_power_w), 'W'],
    ['NP', fixed0(ride.np_w), 'W'],
    ['Energy', fixed0(ride.kj), 'kJ'],
    ['Avg cadence', fixed0(ride.avg_cadence_rpm), 'rpm'],
    ['Avg HR', ride.avg_hr_bpm > 0 ? fixed0(ride.avg_hr_bpm) : '–', ride.avg_hr_bpm > 0 ? 'bpm' : ''],
    ['Avg speed', ride.avg_speed_kmh.toFixed(1), 'km/h'],
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
