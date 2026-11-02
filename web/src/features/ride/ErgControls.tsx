import { useRef, useState } from 'react';
import { MAX_TARGET_W, MIN_TARGET_W } from '@/lib/workout-math';
import type { TickStore } from '@/lib/tick-store';
import { Minus, Plus } from 'lucide-react';
import { Button } from '@/components/Button';
import { Field } from '@/components/Field';
import type { CommandName } from '@/lib/ws-types';
import { ergError } from './StartPicker';
import { isLevelFocus, useRideKey } from './keys';

const step =
  'metric-step h-[4.5rem] min-w-[7rem] flex-1 shrink-0 gap-2 px-3 text-xl font-medium min-[1600px]:h-24 min-[1600px]:text-3xl';

/**
 * ERG target controls. "−10 W" / "+10 W" send level_delta, which the engine
 * turns into a 10 W nudge (in a workout, an adjustment on top of the blocks).
 * These use aria-disabled, not disabled, so focus stays put when the socket drops. Free ERG also takes a typed target. ArrowUp and ArrowDown nudge too (while focus is on the page or the ride control buttons).
 */
export function ErgControls({
  send,
  ticks,
  free = false,
  disabled = false,
  onError,
}: {
  send: (command: CommandName, value?: number) => Promise<void>;
  /** Prefills the free-ERG input with the current target. */
  ticks?: TickStore;
  /** Free ERG (no workout): shows the numeric target input. */
  free?: boolean;
  disabled?: boolean;
  onError?: (e: unknown) => void;
}) {
  const [text, setText] = useState(() => {
    const w = ticks?.latest?.target_power_w ?? 0;
    return w > 0 ? String(Math.round(w)) : '';
  });
  const setBtn = useRef<HTMLButtonElement>(null);
  const watts = Number(text);
  const invalid = text === '' || ergError(watts) !== null;

  function bump(delta: 1 | -1) {
    if (disabled) return;
    send('level_delta', delta).catch((e) => onError?.(e));
  }

  useRideKey((key, _repeat, target) => {
    if (disabled || !isLevelFocus(target)) return false;
    if (key === 'ArrowUp') bump(1);
    else if (key === 'ArrowDown') bump(-1);
    else return false;
    return true;
  });

  return (
    <div className="metric-wide m-wide flex flex-col gap-3 rounded-panel bg-panel p-3">
      <div className="flex items-center gap-3">
        <button type="button" data-nav className={step} aria-disabled={disabled || undefined} onClick={() => bump(-1)}>
          <Minus aria-hidden className="size-7" />
          <span>−10 W</span>
        </button>
        <button type="button" data-nav className={step} aria-disabled={disabled || undefined} onClick={() => bump(1)}>
          <Plus aria-hidden className="size-7" />
          <span>+10 W</span>
        </button>
      </div>
      {free && (
        <form
          className="flex items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (disabled || invalid) return;
            send('set_target_power', watts).catch((err) => onError?.(err));
            setBtn.current?.focus(); // Enter commits and moves on to Set
          }}
        >
          <div className="min-w-0 flex-1">
            <Field
              label="Target power (W)"
              type="number"
              inputMode="numeric"
              min={MIN_TARGET_W}
              max={MAX_TARGET_W}
              step={1}
              value={text}
              onChange={(e) => setText(e.target.value)}
              error={text !== '' ? (ergError(watts) ?? undefined) : undefined}
            />
          </div>
          <Button ref={setBtn} type="submit" variant="primary" aria-disabled={disabled || invalid || undefined} className="mb-px aria-disabled:cursor-not-allowed aria-disabled:opacity-50">
            Set
          </Button>
        </form>
      )}
    </div>
  );
}
