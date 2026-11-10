import { hms } from '@/lib/format';

const POWER_NAMES = ['Active recovery', 'Endurance', 'Tempo', 'Threshold', 'VO2 max', 'Anaerobic', 'Neuromuscular'];
const HR_NAMES = ['Recovery', 'Endurance', 'Tempo', 'Threshold', 'Maximum'];

/** Time in each power or heart-rate zone as horizontal bars. Renders nothing without zone data. */
export function ZoneBars({ title, kind, seconds }: { title: string; kind: 'power' | 'hr'; seconds?: number[] | null }) {
  if (!seconds || seconds.length === 0) return null;
  const names = kind === 'power' ? POWER_NAMES : HR_NAMES;
  const total = seconds.reduce((a, b) => a + b, 0);
  if (total <= 0) return null;
  const top = Math.max(...seconds);
  return (
    <section aria-label={title}>
      <h3 className="mb-2 text-sm text-dim">{title}</h3>
      <ul className="flex flex-col gap-1.5">
        {seconds.map((sec, i) => (
          <li key={i} className="grid grid-cols-[7rem_1fr_6.5rem] items-center gap-3 text-sm">
            <span className="truncate">
              Z{i + 1} <span className="text-dim">{names[i]}</span>
            </span>
            <span className="h-3 rounded bg-raised" aria-hidden>
              <span
                data-testid={`zone-${kind}-${i + 1}`}
                className="block h-full rounded"
                style={{ width: `${(sec / top) * 100}%`, background: `var(--zone-${i + 1})` }}
              />
            </span>
            <span className="numeral text-right">
              {hms(sec)} <span className="text-dim">{Math.round((sec / total) * 100)}%</span>
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}
