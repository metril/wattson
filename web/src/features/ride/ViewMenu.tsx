import { useEffect, useId, useRef, useState } from 'react';
import { Eye } from 'lucide-react';
import { unavailableReason, type Caps, type ViewMode } from '@/lib/view-prefs';

const OPTIONS: { v: ViewMode; label: string }[] = [
  { v: '2d', label: '2D map' },
  { v: '3d', label: '3D flyover' },
  { v: 'photo', label: 'Street photos' },
];

/** A "View" button that opens an inline radio group (no hover, works with arrows and Enter). */
export function ViewMenu(p: {
  value: ViewMode;
  caps: Caps;
  exaggeration: number;
  onChange(v: ViewMode): void;
  onExaggeration(x: number): void;
}) {
  const [open, setOpen] = useState(false);
  const btn = useRef<HTMLButtonElement>(null);
  const id = useId();

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      e.preventDefault();
      setOpen(false);
      btn.current?.focus();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open]);

  return (
    <div className="flex flex-col gap-2">
      <button
        ref={btn}
        type="button"
        aria-expanded={open}
        aria-controls={id}
        onClick={() => setOpen((o) => !o)}
        className="inline-flex min-h-touch items-center justify-center gap-2 self-start rounded-control border border-line bg-raised px-4 text-chalk"
      >
        <Eye aria-hidden className="size-5" />
        View
      </button>
      {open && (
        <div id={id} className="flex flex-col gap-1 rounded-panel border border-line bg-panel p-2">
          <div role="radiogroup" aria-label="Ride view" className="flex flex-col gap-1">
            {OPTIONS.map(({ v, label }) => {
              const why = unavailableReason(v, p.caps);
              return (
                <label
                  key={v}
                  className={`flex min-h-touch items-center gap-3 rounded-control px-3 ${why ? 'text-dim' : 'cursor-pointer text-chalk'}`}
                >
                  <input
                    type="radio"
                    name={`${id}-view`}
                    checked={p.value === v}
                    disabled={!!why}
                    aria-describedby={why ? `${id}-${v}` : undefined}
                    onChange={() => p.onChange(v)}
                    className="size-5 accent-accent"
                  />
                  <span className="flex flex-col">
                    {label}
                    {why && (
                      <span id={`${id}-${v}`} className="text-sm text-dim">
                        {why}
                      </span>
                    )}
                  </span>
                </label>
              );
            })}
          </div>
          {p.value === '3d' && (
            <label className="flex min-h-touch items-center gap-3 px-3 text-chalk">
              Terrain height
              <input
                type="range"
                min={1}
                max={3}
                step={0.25}
                value={p.exaggeration}
                onChange={(e) => p.onExaggeration(Number(e.target.value))}
                className="h-12 min-w-0 flex-1 accent-accent"
              />
              <span className="w-10 text-right tabular-nums">{p.exaggeration.toFixed(2).replace(/0$/, '')}x</span>
            </label>
          )}
        </div>
      )}
    </div>
  );
}
