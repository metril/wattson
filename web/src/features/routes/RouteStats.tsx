import type { components } from '@/lib/openapi';
import { fixed0, km } from '@/lib/format';

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
export function RouteStats({ route }: { route: Route }) {
  const items: [string, string, string][] = [
    ['Distance', km(route.distance_m), 'km'],
    ['Ascent', fixed0(route.ascent_m), 'm'],
    ['Descent', fixed0(route.descent_m), 'm'],
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
