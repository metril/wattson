import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react';
import { Eye } from 'lucide-react';
import { unavailableReason, type Caps, type ViewMode } from '@/lib/view-prefs';

const OPTIONS: { v: ViewMode; label: string }[] = [
  { v: '2d', label: '2D map' },
  { v: '3d', label: '3D flyover' },
  { v: 'photo', label: 'Street photos' },
];

/**
 * A "View" button that opens an inline radio group (no hover, works with
 * arrows and Enter). Arrows (and Home/End) only move focus; Enter or Space selects, so a
 * D-pad pass over 3D never builds a WebGL map on the way.
 */
export function ViewMenu(p: {
  value: ViewMode;
  caps: Caps;
  exaggeration: number;
  onChange(v: ViewMode): void;
  onExaggeration(x: number): void;
}) {
  const [open, setOpen] = useState(false);
  const btn = useRef<HTMLButtonElement>(null);
  const group = useRef<HTMLDivElement>(null);
  const id = useId();

  useEffect(() => {
    if (!open) return;
    // On document, so it runs (and marks the key used) before the page's window-level "back" handler.
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      e.preventDefault();
      setOpen(false);
      btn.current?.focus();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [open]);

  const onArrow = (e: KeyboardEvent<HTMLDivElement>) => {
    const dir = e.key === 'ArrowDown' || e.key === 'ArrowRight' ? 1 : e.key === 'ArrowUp' || e.key === 'ArrowLeft' ? -1 : 0;
    const edge = e.key === 'Home' || e.key === 'End';
    if (!dir && !edge) return;
    e.preventDefault(); // also keeps the ride's arrow keys from changing resistance
    const items = Array.from(group.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? []);
    if (!items.length) return;
    if (edge) {
      items[e.key === 'Home' ? 0 : items.length - 1].focus();
      return;
    }
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    items[(at + dir + items.length) % items.length].focus();
  };

  return (
    <div className="flex flex-col gap-2">
      <button
        ref={btn}
        type="button"
        data-nav
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        onClick={() => setOpen((o) => !o)}
        className="inline-flex min-h-touch items-center justify-center gap-2 self-start rounded-control border border-line bg-raised px-4 text-chalk"
      >
        <Eye aria-hidden className="size-5" />
        View
      </button>
      {open && (
        <div id={id} className="flex flex-col gap-1 rounded-panel border border-line bg-panel p-2">
          <div ref={group} role="radiogroup" aria-label="Ride view" onKeyDown={onArrow} className="flex flex-col gap-1">
            {OPTIONS.map(({ v, label }) => {
              const why = unavailableReason(v, p.caps);
              const checked = p.value === v;
              return (
                <button
                  key={v}
                  type="button"
                  data-nav
                  role="radio"
                  aria-checked={checked}
                  aria-labelledby={`${id}-${v}-l`}
                  aria-describedby={why ? `${id}-${v}-d` : undefined}
                  tabIndex={checked ? 0 : -1}
                  disabled={!!why}
                  onClick={() => p.onChange(v)}
                  className={`flex min-h-touch items-center gap-3 rounded-control px-3 text-left ${why ? 'text-dim' : 'text-chalk'}`}
                >
                  <span
                    aria-hidden
                    className={`flex size-5 shrink-0 items-center justify-center rounded-full border-2 ${checked ? 'border-accent' : 'border-line'}`}
                  >
                    {checked && <span className="size-2.5 rounded-full bg-accent" />}
                  </span>
                  <span className="flex flex-col">
                    <span id={`${id}-${v}-l`}>{label}</span>
                    {why && (
                      <span id={`${id}-${v}-d`} className="text-sm text-dim">
                        {why}
                      </span>
                    )}
                  </span>
                </button>
              );
            })}
          </div>
          {p.value === '3d' && (
            <label className="flex min-h-touch items-center gap-3 px-3 text-chalk">
              Terrain height
              <input
                type="range"
                data-nav
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
