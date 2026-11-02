import { useEffect, useRef, useState } from 'react';
import { Pause, Play, Square } from 'lucide-react';
import { Button } from '@/components/Button';
import type { CommandName, RideState } from '@/lib/ws-types';
import { useRideKey } from './keys';

const PRIMARY: Partial<Record<RideState, { command: CommandName; label: string }>> = {
  ready: { command: 'start', label: 'Start' },
  riding: { command: 'pause', label: 'Pause' },
  paused: { command: 'resume', label: 'Resume' },
};

const big = 'h-[4.5rem] flex-1 text-xl min-[1600px]:h-24 min-[1600px]:text-3xl';
const CONFIRM_MS = 4000;
// After an ack the state flips on the next tick; hold the buttons until then.
const SETTLE_MS = 2000;

/**
 * Start / Pause / Resume / Stop for the current ride state. A button stays
 * disabled until its command is acknowledged. Stop asks for a second tap.
 * Enter or Space runs the primary action (Start, Pause or Resume).
 */
export function RideControls({
  state,
  send,
  disabled = false,
  onError,
}: {
  state: RideState;
  send: (command: CommandName) => Promise<void>;
  disabled?: boolean;
  onError?: (e: unknown) => void;
}) {
  const [busy, setBusy] = useState<CommandName | null>(null);
  const [armed, setArmed] = useState(false);
  const primary = PRIMARY[state];
  const canStop = state === 'riding' || state === 'paused';

  useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), CONFIRM_MS);
    return () => clearTimeout(t);
  }, [armed]);
  useEffect(() => setArmed(false), [state]);

  // busy lasts from the tap until the state changes after the ack, or SETTLE_MS.
  const locked = useRef(false);
  const acked = useRef(false);
  const settle = useRef<ReturnType<typeof setTimeout>>();
  const release = () => {
    clearTimeout(settle.current);
    locked.current = acked.current = false;
    setBusy(null);
  };
  useEffect(() => {
    if (acked.current) release();
  }, [state]);
  useEffect(() => () => clearTimeout(settle.current), []);

  // A button that disables itself while its command is in flight drops focus; give it back.
  const focused = useRef<HTMLElement | null>(null);
  const remember = (e: { currentTarget: HTMLElement }) => (focused.current = e.currentTarget);
  useEffect(() => {
    if (busy !== null || (document.activeElement && document.activeElement !== document.body)) return;
    const back = focused.current?.isConnected ? focused.current : document.querySelector<HTMLElement>('[data-primary]');
    back?.focus();
  }, [busy]);

  async function run(command: CommandName) {
    if (locked.current || disabled) return;
    locked.current = true;
    setBusy(command);
    try {
      await send(command);
      acked.current = true;
      settle.current = setTimeout(release, SETTLE_MS);
    } catch (e) {
      onError?.(e);
      release();
    }
  }

  function stop() {
    if (!armed) {
      setArmed(true);
      return;
    }
    setArmed(false);
    void run('stop');
  }

  useRideKey((key, repeat) => {
    if ((key !== 'Enter' && key !== ' ') || !primary) return false;
    // A held key must not toggle start/pause over and over.
    if (!repeat) void run(primary.command);
    return true;
  });

  if (state === 'finished') {
    return (
      <div className={shell}>
        <p className="flex-1 py-3 text-lg">Ride finished. Release the bike when you are done.</p>
      </div>
    );
  }
  if (!primary) return null;

  const Icon = state === 'ready' || state === 'paused' ? Play : Pause;
  return (
    <div className={shell}>
      <Button
        variant="primary"
        data-primary
        data-level
        onFocus={remember}
        className={big}
        disabled={disabled || busy !== null}
        onClick={() => void run(primary.command)}
      >
        <Icon aria-hidden className="size-7" />
        {primary.label}
      </Button>
      {canStop && (
        <Button
          variant={armed ? 'danger' : 'secondary'}
          data-level
          onFocus={remember}
          className={big}
          disabled={disabled || busy !== null}
          onClick={stop}
        >
          <Square aria-hidden className="size-6" />
          {armed ? 'Tap again to stop' : 'Stop'}
        </Button>
      )}
    </div>
  );
}

// Phones: a bar pinned above the tab bar. Wider screens: part of the page.
const shell =
  'fixed inset-x-0 bottom-[calc(3.5rem+env(safe-area-inset-bottom,0px))] z-10 flex gap-3 border-t border-line bg-panel p-3 md:static md:z-auto md:border-0 md:bg-transparent md:p-0';
