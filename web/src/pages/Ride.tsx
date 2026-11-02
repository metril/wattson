import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { useSearchParams } from 'react-router';
import { Banner } from '@/components/Banner';
import { BikeBar } from '@/features/ride/BikeBar';
import { LevelControls } from '@/features/ride/LevelControls';
import { PrefetchPill } from '@/features/ride/PrefetchPill';
import { MetricGrid } from '@/features/ride/MetricGrid';
import { RideControls } from '@/features/ride/RideControls';
import { ErgControls } from '@/features/ride/ErgControls';
import { choiceFromParams, ergError, StartPicker, type StartChoice } from '@/features/ride/StartPicker';
import { RouteView, useRouteId } from '@/features/ride/RouteView';
import { useTickValue } from '@/features/ride/useTickValue';
import { WorkoutView } from '@/features/ride/WorkoutView';
import { notifyUnauthorized } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { createRideSocket } from '@/lib/ride-socket';
import type { CommandName, Tick } from '@/lib/ws-types';

const ALERT_MS = 10_000;

/**
 * Ride screen: live readouts, resistance and ride controls over the WebSocket.
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

  // The ride screen is always dark (dim rooms, TV glare); other pages follow the system theme.
  useEffect(() => {
    document.documentElement.classList.add('ride-dark');
    return () => document.documentElement.classList.remove('ride-dark');
  }, []);

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

  // What Start rides: picked before Start (from ?route= or ?workout=), then whatever the ticks say.
  const [params] = useSearchParams();
  const [choice, setChoice] = useState<StartChoice>(() => choiceFromParams(params));
  const routeId = useRouteId(socket.ticks);
  const routeMode = routeId !== 0;
  const workoutId = useTickValue(socket.ticks, (t) => t.workout_id ?? 0, 0);
  const mode = useTickValue(socket.ticks, (t) => t.mode, '');
  const control = useCallback(
    (command: CommandName) => {
      if (command !== 'start') return send(command);
      switch (choice.kind) {
        case 'route':
          return send('start', 0, { route_id: choice.id });
        case 'workout':
          return send('start', 0, { workout_id: choice.id });
        case 'erg': {
          const bad = ergError(choice.watts);
          return bad ? Promise.reject(new Error(bad)) : send('start', 0, { target_power_w: choice.watts });
        }
        default:
          return send('start');
      }
    },
    [send, choice],
  );

  // "Route/Workout complete" only when the end was reached (a manual Stop is not), until the next Start.
  const [complete, setComplete] = useState<'route' | 'workout' | null>(null);
  useEffect(() => {
    const apply = (t: Tick) => {
      if (t.state === 'finished') {
        if (t.workout_id > 0) setComplete(t.block_count > 0 && t.block_index >= t.block_count - 1 && t.block_remaining_s <= 0 ? 'workout' : null);
        else setComplete(t.route_id > 0 && t.route_length_m > 0 && t.distance_m >= t.route_length_m ? 'route' : null);
      } else if (t.state === 'riding') {
        setComplete(null);
      }
    };
    if (socket.ticks.latest) apply(socket.ticks.latest);
    return socket.ticks.subscribe(apply);
  }, [socket]);

  const { connected, state, bike } = status;
  // Stays mounted from picking the route into the ride, so pressing Start does not cancel the save.
  const pillRoute = routeMode ? routeId : state === 'ready' && choice.kind === 'route' ? choice.id : 0;
  const ergMode = mode === 'erg';
  const active = state === 'ready' || state === 'riding' || state === 'paused';

  return (
    <div className={`ride-screen ${routeMode || workoutId !== 0 ? '' : 'ride-flat'}`}>
      <h1 className="sr-only">Ride</h1>
      <div className="ride-top">
        <BikeBar bike={bike} connected={connected} state={state} ticks={socket.ticks} />
        {alert && (
          <Banner tone="warn">
            <p className="text-lg font-medium">{alert}</p>
          </Banner>
        )}
        {cmdError && <Banner tone="error">{cmdError}</Banner>}
        {state === 'ready' && <StartPicker value={choice} onChange={setChoice} />}
        {pillRoute !== 0 && <PrefetchPill routeId={pillRoute} />}
        {complete && (
          <Banner tone="ok">
            <p className="text-lg font-medium">{complete === 'workout' ? 'Workout complete' : 'Route complete'}</p>
          </Banner>
        )}
      </div>
      {(routeMode || workoutId !== 0) && (
        <div className="ride-view">
          {routeMode && <RouteView routeId={routeId} ticks={socket.ticks} />}
          {workoutId !== 0 && <WorkoutView workoutId={workoutId} ticks={socket.ticks} riderId={bike?.rider_id} />}
        </div>
      )}
      <div className="ride-metrics">
        <MetricGrid ticks={socket.ticks} showLevel={false} route={routeMode} erg={ergMode} hr={bike?.hr}>
          {ergMode ? (
            <ErgControls send={send} ticks={socket.ticks} free={workoutId === 0} disabled={!connected || !active} onError={onError} />
          ) : (
            <LevelControls
              socket={levelSocket}
              route={routeMode || mode === 'interval'}
              disabled={!connected || !active}
              onError={onError}
            />
          )}
        </MetricGrid>
      </div>
      <div className="ride-controls">
        <RideControls state={state} send={control} disabled={!connected} onError={onError} />
      </div>
    </div>
  );
}
