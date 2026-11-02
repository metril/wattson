import { Minus, Plus } from 'lucide-react';
import { useTickText, type TickStore } from '@/lib/tick-store';
import type { RideSocket } from '@/lib/ride-socket';
import { useRideKey } from './keys';

const step =
  'inline-flex size-[4.5rem] shrink-0 items-center justify-center rounded-control border border-line bg-raised text-chalk hover:border-accent active:bg-line disabled:cursor-not-allowed disabled:opacity-50 min-[1600px]:size-24';

const pickLevel = (t: { level: number }) => `L${t.level}`;
const pickReported = (t: { reported_level: number }) => `bike reports ${t.reported_level > 0 ? t.reported_level : '–'}`;

/**
 * Resistance − / +. The number between them is the engine's level from the
 * latest tick, never a guess from the taps, so it can't drift from the bike.
 * ArrowUp/Right raise it, ArrowDown/Left lower it.
 */
export function LevelControls({
  socket,
  disabled = false,
  onError,
}: {
  socket: Pick<RideSocket, 'send'> & { ticks: TickStore };
  disabled?: boolean;
  onError?: (e: unknown) => void;
}) {
  const level = useTickText(socket.ticks, pickLevel);
  const reported = useTickText(socket.ticks, pickReported);

  function bump(delta: 1 | -1) {
    if (disabled) return;
    socket.send('level_delta', delta).catch((e) => onError?.(e));
  }

  useRideKey((key) => {
    if (disabled) return false;
    if (key === 'ArrowUp' || key === 'ArrowRight') bump(1);
    else if (key === 'ArrowDown' || key === 'ArrowLeft') bump(-1);
    else return false;
    return true;
  });

  return (
    <div className="col-span-2 flex items-center justify-between gap-3 rounded-panel border border-line bg-panel p-3">
      <button type="button" className={step} aria-label="Decrease level" disabled={disabled} onClick={() => bump(-1)}>
        <Minus aria-hidden className="size-9" />
      </button>
      <div className="flex min-w-0 flex-col items-center">
        <span className="text-sm font-medium text-dim">Resistance</span>
        <span ref={level} className="numeral text-numeral-md min-[1600px]:text-[clamp(3rem,6vw,7rem)]" />
        <span ref={reported} className="text-sm text-dim" />
      </div>
      <button type="button" className={step} aria-label="Increase level" disabled={disabled} onClick={() => bump(1)}>
        <Plus aria-hidden className="size-9" />
      </button>
    </div>
  );
}
