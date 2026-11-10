import { hms } from '@/lib/format';
import { pctLabel, rangeLabel, zoneRanges, type ZoneKind } from '@/lib/zones';

/**
 * Time in each power or heart-rate zone: a 100% stacked bar, then one row per zone with its
 * name, watt or bpm range and share. `base` is FTP (power) or max HR (heart rate); without it
 * the rows show names only. Renders nothing without zone data.
 */
export function ZoneBars({ title, kind, seconds, base }: { title: string; kind: ZoneKind; seconds?: number[] | null; base?: number | null }) {
  if (!seconds || seconds.length === 0) return null;
  const total = seconds.reduce((a, b) => a + b, 0);
  if (total <= 0) return null;
  const top = Math.max(...seconds);
  const ranges = zoneRanges(kind, base);
  const unit = kind === 'power' ? 'W' : 'bpm';
  const of = kind === 'power' ? 'FTP' : 'max HR';
  return (
    <section aria-label={title} className="flex flex-col gap-3">
      <h3 className="label">{title}</h3>
      <div aria-hidden className="flex h-3 gap-px overflow-hidden rounded-full bg-sunken">
        {seconds.map((sec, i) => (
          <span key={i} className="block h-full" style={{ flexGrow: sec, flexBasis: 0, background: `var(--zone-${i + 1})` }} />
        ))}
      </div>
      <ul className="flex flex-col gap-2">
        {seconds.map((sec, i) => {
          const r = ranges[i];
          const range = r ? rangeLabel(r, unit) : '';
          return (
            <li key={i} className="grid grid-cols-[minmax(9rem,auto)_1fr_6.5rem] items-center gap-x-3 text-sm">
              <span className="flex min-w-0 flex-col leading-tight">
                <span>
                  <span className="numeral" style={{ color: `var(--zone-${i + 1})` }}>Z{i + 1}</span>{' '}
                  <span>{r?.name}</span>
                </span>
                {range && (
                  <span className="text-xs text-dim">
                    {range} · {pctLabel(r, of)}
                  </span>
                )}
              </span>
              <span className="h-2.5 rounded-full bg-sunken" aria-hidden>
                <span
                  data-testid={`zone-${kind}-${i + 1}`}
                  className="block h-full rounded-full"
                  style={{ width: `${(sec / top) * 100}%`, background: `var(--zone-${i + 1})` }}
                />
              </span>
              <span className="numeral text-right">
                {hms(sec)} <span className="text-dim">{Math.round((sec / total) * 100)}%</span>
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
