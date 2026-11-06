import type { components } from '@/lib/openapi';
import { distUnit, elevUnit, toDist, toElev, type Units } from '@/lib/format';

type Route = components['schemas']['RouteOut'];

const SOURCE: Record<Route['source'], string> = {
  gpx: 'GPX file',
  tcx: 'TCX file',
  fit: 'FIT file',
  draw: 'Drawn',
  synthetic: 'Synthetic',
};

export const sourceLabel = (s: Route['source']) => SOURCE[s];

/** Distance, climb and steepest grades of a route. */
export function RouteStats({ route, units = 'metric' }: { route: Route; units?: Units }) {
  const items: [string, string, string][] = [
    ['Distance', toDist(route.distance_m, units).toFixed(2), distUnit(units)],
    ['Ascent', toElev(route.ascent_m, units).toFixed(0), elevUnit(units)],
    ['Descent', toElev(route.descent_m, units).toFixed(0), elevUnit(units)],
    ['Steepest climb', route.max_grade_pct.toFixed(1), '%'],
    ['Steepest descent', route.min_grade_pct.toFixed(1), '%'],
  ];
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3 lg:grid-cols-2 2xl:grid-cols-3">
      {items.map(([label, value, unit]) => (
        <div key={label}>
          <dt className="text-sm text-dim">{label}</dt>
          <dd className="numeral text-3xl">
            {value}
            <span className="ml-1 text-base font-medium text-dim">{unit}</span>
          </dd>
        </div>
      ))}
    </dl>
  );
}
