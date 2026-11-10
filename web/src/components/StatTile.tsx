import type { ReactNode } from 'react';

export type MetricColor = 'power' | 'hr' | 'cadence' | 'speed' | 'elev';

const colors: Record<MetricColor, string> = {
  power: 'text-m-power',
  hr: 'text-m-hr',
  cadence: 'text-m-cadence',
  speed: 'text-m-speed',
  elev: 'text-m-elev',
};

/** A labelled number: micro-label, big value, unit, and an optional delta line. */
export function StatTile({
  label,
  value,
  unit,
  color,
  delta,
  size = 'sm',
  className = '',
}: {
  label: string;
  value: ReactNode;
  unit?: string;
  color?: MetricColor;
  delta?: ReactNode;
  size?: 'sm' | 'lg';
  className?: string;
}) {
  return (
    <div className={`flex min-w-0 flex-col gap-0.5 ${className}`}>
      <dt className="label">{label}</dt>
      <dd className="m-0 flex items-baseline gap-1">
        <span className={`numeral ${size === 'lg' ? 'text-4xl sm:text-5xl' : 'text-numeral-sm'} leading-none ${color ? colors[color] : 'text-chalk'}`}>
          {value}
        </span>
        {unit && <span className="text-sm font-medium text-dim">{unit}</span>}
      </dd>
      {delta && <dd className="m-0 text-xs text-dim">{delta}</dd>}
    </div>
  );
}
