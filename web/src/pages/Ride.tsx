import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { Banner } from '@/components/Banner';
import { BikeBar } from '@/features/ride/BikeBar';
import { LevelControls } from '@/features/ride/LevelControls';
import { MetricGrid } from '@/features/ride/MetricGrid';
import { RideControls } from '@/features/ride/RideControls';
import { notifyUnauthorized } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { createRideSocket } from '@/lib/ride-socket';

const ALERT_MS = 10_000;

/**
 * Free ride: live readouts, resistance and ride controls over the WebSocket.
 * Ticks bypass React; this component re-renders only when the connection,
 * bike, ride state or alert changes.
 */
export function RidePage() {
  const [socket] = useState(() =>
    createRideSocket(() => notifyUnauthorized(location.pathname + location.search)),
  );
  useEffect(() => {
    socket.start();
    return () => socket.close();
  }, [socket]);

  const subscribe = useCallback((cb: () => void) => socket.onStatus(cb), [socket]);
  const status = useSyncExternalStore(subscribe, () => socket.getStatus());

  const [alert, setAlert] = useState<string | null>(null);
  const shownAt = useRef(0);
  // An alert stays while the tick carries it, and for at least ALERT_MS after it appeared.
  useEffect(() => {
    if (status.alert) {
      shownAt.current = Date.now();
      setAlert(status.alert);
      return;
    }
    const left = Math.max(0, ALERT_MS - (Date.now() - shownAt.current));
    const t = setTimeout(() => setAlert(null), left);
    return () => clearTimeout(t);
  }, [status.alert]);

  const [cmdError, setCmdError] = useState<string | null>(null);
  const onError = useCallback((e: unknown) => setCmdError(errMessage(e)), []);
  const send = useCallback(
    (...args: Parameters<typeof socket.send>) => {
      setCmdError(null);
      return socket.send(...args);
    },
    [socket],
  );
  const levelSocket = useMemo(() => ({ send, ticks: socket.ticks }), [send, socket]);

  const { connected, state, bike } = status;
  const active = state === 'ready' || state === 'riding' || state === 'paused';

  return (
    <div className="mx-auto flex w-full max-w-[120rem] flex-col gap-3 pb-28 md:gap-4 md:pb-0">
      <h1 className="sr-only">Ride</h1>
      <BikeBar bike={bike} connected={connected} state={state} ticks={socket.ticks} />
      {alert && (
        <Banner tone="warn">
          <p className="text-lg font-medium">{alert}</p>
        </Banner>
      )}
      {cmdError && <Banner tone="error">{cmdError}</Banner>}
      <MetricGrid ticks={socket.ticks} showLevel={false}>
        <LevelControls socket={levelSocket} disabled={!connected || !active} onError={onError} />
      </MetricGrid>
      <RideControls state={state} send={send} disabled={!connected} onError={onError} />
    </div>
  );
}
