import { Minus, Plus } from 'lucide-react';
import { useTickText, type TickStore } from '@/lib/tick-store';
import type { RideSocket } from '@/lib/ride-socket';
import { isLevelFocus, useRideKey } from './keys';

const step = 'metric-step'; // 72 px, see index.css

const pickLevel = (t: { level: number }) => `L${t.level}`;
const pickBias = (t: { bias: number }) => `Bias ${t.bias > 0 ? '+' : ''}${Math.round(t.bias)}`;
const pickReported = (t: { reported_level: number }) => `bike reports ${t.reported_level > 0 ? t.reported_level : '–'}`;

/**
 * Resistance − / +. The number between them is the engine's level from the
 * latest tick, never a guess from the taps, so it can't drift from the bike.
 * ArrowUp raises it, ArrowDown lowers it (while focus is on the page or on these
 * buttons; ArrowLeft/Right move focus). In route mode the same
 * level_delta nudges the bias on top of the route's own level, so the buttons
 * read Easier/Harder and the bias is shown.
 */
export function LevelControls({
  socket,
  disabled = false,
  route = false,
  onError,
}: {
  socket: Pick<RideSocket, 'send'> & { ticks: TickStore };
  disabled?: boolean;
  route?: boolean;
  onError?: (e: unknown) => void;
}) {
  const level = useTickText(socket.ticks, pickLevel);
  const reported = useTickText(socket.ticks, pickReported);
  const bias = useTickText(socket.ticks, pickBias);

  function bump(delta: 1 | -1) {
    if (disabled) return;
    socket.send('level_delta', delta).catch((e) => onError?.(e));
  }

  useRideKey((key, _repeat, target) => {
    if (disabled || !isLevelFocus(target)) return false;
    if (key === 'ArrowUp') bump(1);
    else if (key === 'ArrowDown') bump(-1);
    else return false;
    return true;
  });

  // `contents`: the three parts are cells of the metric grid, ordered by the layout (Level, then - and +).
  return (
    <div className="contents">
      <div className="metric-tile m-level">
        <span className="metric-label">Resistance</span>
        <span ref={level} className="numeral metric-value ride-level-up" />
        {/* Always mounted: useTickText binds its ref once, so a span added later would stay empty. */}
        <span ref={bias} hidden={!route} className="numeral text-lg" />
        <span ref={reported} className="text-sm text-dim" />
      </div>
      <button type="button" data-nav data-level className={`${step} m-minus`} aria-label={route ? 'Easier' : 'Decrease level'} disabled={disabled} onClick={() => bump(-1)}>
        <Minus aria-hidden className="size-9" />
      </button>
      <button type="button" data-nav data-level className={`${step} m-plus`} aria-label={route ? 'Harder' : 'Increase level'} disabled={disabled} onClick={() => bump(1)}>
        <Plus aria-hidden className="size-9" />
      </button>
    </div>
  );
}
