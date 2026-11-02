import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { useSearchParams } from 'react-router';
import { Banner } from '@/components/Banner';
import { BikeBar } from '@/features/ride/BikeBar';
import { LevelControls } from '@/features/ride/LevelControls';
import { MetricGrid } from '@/features/ride/MetricGrid';
import { RideControls } from '@/features/ride/RideControls';
import { RoutePicker } from '@/features/ride/RoutePicker';
import { RouteView, useRouteId } from '@/features/ride/RouteView';
import { notifyUnauthorized } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { createRideSocket } from '@/lib/ride-socket';
import type { CommandName, Tick } from '@/lib/ws-types';

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

  // Route mode: picked before Start (from ?route=), then whatever the ticks say.
  const [params] = useSearchParams();
  const [picked, setPicked] = useState<number | null>(() => {
    const n = Number(params.get('route'));
    return Number.isInteger(n) && n > 0 ? n : null;
  });
  const routeId = useRouteId(socket.ticks);
  const routeMode = routeId !== 0;
  const control = useCallback(
    (command: CommandName) => (command === 'start' && picked !== null ? send('start', 0, picked) : send(command)),
    [send, picked],
  );

  // "Route complete" only when the finish line was reached (a manual Stop is not), until the next Start.
  const [complete, setComplete] = useState(false);
  useEffect(() => {
    const apply = (t: Tick) => {
      if (t.state === 'finished') {
        setComplete(t.route_id > 0 && t.route_length_m > 0 && t.distance_m >= t.route_length_m);
      } else if (t.state === 'riding') {
        setComplete(false);
      }
    };
    if (socket.ticks.latest) apply(socket.ticks.latest);
    return socket.ticks.subscribe(apply);
  }, [socket]);

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
      {state === 'ready' && <RoutePicker value={picked} onChange={setPicked} />}
      {complete && (
        <Banner tone="ok">
          <p className="text-lg font-medium">Route complete</p>
        </Banner>
      )}
      {routeMode && <RouteView routeId={routeId} ticks={socket.ticks} />}
      <MetricGrid ticks={socket.ticks} showLevel={false} route={routeMode} hr={bike?.hr}>
        <LevelControls
          socket={levelSocket}
          route={routeMode}
          disabled={!connected || !active}
          onError={onError}
        />
      </MetricGrid>
      <RideControls state={state} send={control} disabled={!connected} onError={onError} />
    </div>
  );
}
