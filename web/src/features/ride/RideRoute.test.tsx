import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RidePage } from '@/pages/Ride';
import { apiError, profileFixture, renderWithClient, routeFixture, stubApi } from '@/test-utils';
import { bikeFixture, FakeWebSocket, tickFixture } from '@/test-ws';
import { positionAt } from '@/lib/route-math';
import type { components } from '@/lib/openapi';
import type { Tick } from '@/lib/ws-types';

const h = vi.hoisted(() => ({ setMarker: vi.fn(), mounts: 0 }));
vi.mock('@/features/map/MapView', async () => {
  const React = await import('react');
  return {
    STYLE_URL: 'x',
    default: (p: { onReady(a: unknown): void }) => {
      React.useEffect(() => {
        h.mounts++;
        p.onReady({
          setLine: vi.fn(),
          fit: vi.fn(),
          setMarker: h.setMarker,
          setWaypoints: vi.fn(),
          setTiles: vi.fn(),
          getCenter: () => [0, 0],
        });
        // eslint-disable-next-line react-hooks/exhaustive-deps
      }, []);
      return <div data-testid="mapview" />;
    },
  };
});

vi.mock('@/features/ride/Ride3D', () => ({
  default: (p: { exaggeration: number; tier: string; onFallback(r: string): void }) => (
    <div data-testid="ride3d" data-ex={p.exaggeration} data-tier={p.tier}>
      <button onClick={() => p.onFallback('slow')}>mock slow</button>
      <button onClick={() => p.onFallback('context_lost')}>mock lost</button>
    </div>
  ),
}));

vi.mock('@/features/ride/PhotoView', () => ({
  default: (p: { photos: unknown[]; reducedMotion: boolean }) => (
    <div data-testid="photoview" data-n={p.photos.length} data-reduced={String(p.reducedMotion)} />
  ),
}));

/** matchMedia for the two queries RouteView watches; the returned setter fires change events. */
function mockMedia(init: { wide: boolean; reduced: boolean }) {
  const on = { ...init };
  const subs = { wide: new Set<() => void>(), reduced: new Set<() => void>() };
  vi.stubGlobal('matchMedia', (q: string) => {
    const k = q.includes('reduced-motion') ? 'reduced' : 'wide';
    return {
      get matches() {
        return on[k];
      },
      media: q,
      addEventListener: (_: string, fn: () => void) => subs[k].add(fn),
      removeEventListener: (_: string, fn: () => void) => subs[k].delete(fn),
    };
  });
  return (k: 'wide' | 'reduced', v: boolean) => {
    on[k] = v;
    act(() => subs[k].forEach((f) => f()));
  };
}

const withWebgl = () =>
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(((type: string) =>
    type === 'webgl2' ? {} : null) as unknown as HTMLCanvasElement['getContext']);

beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, value: 400 });
});
afterAll(() => {
  delete (HTMLElement.prototype as unknown as Record<string, unknown>).clientWidth;
});
beforeEach(() => {
  FakeWebSocket.reset();
  h.setMarker.mockClear();
  h.mounts = 0;
  vi.stubGlobal('WebSocket', FakeWebSocket);
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  localStorage.clear();
});

const rider = { id: 1, name: 'Ann', min_level: 1, max_level: 16 } as components['schemas']['RiderOut'];
const profile = profileFixture({ route_id: 3, length_m: 900, step_m: 10, lon: [7, 7.05, 7.1], lat: [45, 45.05, 45.1] });
const routes = [routeFixture({ id: 2, name: 'Flat' }), routeFixture({ id: 3, name: 'Climb' })];

async function setup(route = '/?route=3', over: Record<string, Parameters<typeof stubApi>[0][string]> = {}) {
  const stub = stubApi({
    'GET /api/riders': [200, [rider]],
    'GET /api/routes': [200, routes],
    'GET /api/routes/3': [200, routeFixture({ id: 3, name: 'Climb' })],
    'GET /api/routes/3/profile': [200, profile],
    ...over,
  });
  renderWithClient(<RidePage />, route);
  const ws = FakeWebSocket.last;
  act(() => {
    ws.open();
    ws.receive({ type: 'state', bike: bikeFixture({ state: 'ready' }), tick: tickFixture({ state: 'ready' }) });
  });
  await screen.findByRole('option', { name: 'Ann' });
  return { ws, stub };
}
const tick = (ws: FakeWebSocket, over: Partial<Tick>) =>
  act(() => ws.receive({ type: 'tick', tick: tickFixture({ state: 'riding', route_id: 3, route_length_m: 900, ...over }) }));

describe('Ride route mode', () => {
  it('preselects ?route= and Start sends that route_id', async () => {
    const { ws } = await setup();
    const picker = await screen.findByLabelText('Ride');
    await waitFor(() => expect(picker).toHaveValue('route:3'));
    await userEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(ws.commands.at(-1)).toMatchObject({ command: 'start', value: 0, route_id: 3 });
  });

  it('Free ride sends no route_id', async () => {
    const { ws } = await setup('/');
    const picker = await screen.findByLabelText('Ride');
    await waitFor(() => expect(screen.getByRole('option', { name: 'Climb' })).toBeInTheDocument());
    expect(picker).toHaveValue('free');
    await userEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(ws.commands.at(-1)).not.toHaveProperty('route_id');
  });

  it('fetches the profile once over many ticks and moves the marker and data-pos', async () => {
    const { ws, stub } = await setup();
    for (let i = 0; i < 10; i++) tick(ws, { distance_m: i });
    await screen.findByTestId('mapview');
    tick(ws, { distance_m: 15 });
    const [lon, lat] = positionAt(profile, 15);
    await waitFor(() => expect(h.setMarker).toHaveBeenLastCalledWith([lon, lat]));
    await waitFor(() => expect(screen.getByTestId('route-map')).toHaveAttribute('data-pos', `${lat},${lon}`));
    expect(stub.calls.filter((c) => c.key === 'GET /api/routes/3/profile')).toHaveLength(1);
    expect(screen.getByTestId('route-pos')).toBeInTheDocument();
  });

  it('a synthetic route has no map', async () => {
    const { ws } = await setup('/?route=3', {
      'GET /api/routes/3': [200, routeFixture({ id: 3, source: 'synthetic' })],
    });
    tick(ws, { distance_m: 5 });
    await screen.findByTestId('route-pos');
    expect(screen.queryByTestId('mapview')).not.toBeInTheDocument();
    expect(screen.queryByTestId('route-map')).not.toBeInTheDocument();
  });

  it('Harder/Easier send level_delta and a tick shows the bias', async () => {
    const { ws } = await setup();
    tick(ws, { distance_m: 5 });
    await userEvent.click(await screen.findByRole('button', { name: 'Harder' }));
    expect(ws.commands.at(-1)).toMatchObject({ command: 'level_delta', value: 1 });
    (document.activeElement as HTMLElement).blur(); // the buttons are plain targets; the page takes Up/Down
    await userEvent.keyboard('{ArrowDown}');
    expect(ws.commands.at(-1)).toMatchObject({ command: 'level_delta', value: -1 });
    tick(ws, { bias: 1 });
    await screen.findByText('Bias +1');
  });

  it('shows Route complete when the route ride finishes', async () => {
    const { ws } = await setup();
    tick(ws, { distance_m: 900 });
    tick(ws, { state: 'finished', distance_m: 900 });
    expect(await screen.findByText('Route complete')).toBeInTheDocument();
  });

  it('a manual Stop before the finish shows nothing route-specific', async () => {
    const { ws } = await setup();
    tick(ws, { distance_m: 400 });
    tick(ws, { state: 'finished', distance_m: 400 });
    await screen.findByRole('button', { name: 'Harder' });
    expect(screen.queryByText('Route complete')).not.toBeInTheDocument();
  });

  it('keeps Route complete after the route clears, until the next Start', async () => {
    const { ws } = await setup();
    tick(ws, { state: 'finished', distance_m: 900 });
    expect(await screen.findByText('Route complete')).toBeInTheDocument();
    act(() => ws.receive({ type: 'tick', tick: tickFixture({ state: 'ready', route_id: 0 }) }));
    expect(screen.getByText('Route complete')).toBeInTheDocument();
    tick(ws, { distance_m: 0 });
    await waitFor(() => expect(screen.queryByText('Route complete')).not.toBeInTheDocument());
  });

  it('a deleted route degrades to the plain metrics', async () => {
    const { ws } = await setup('/?route=3', {
      'GET /api/routes/3': apiError(404, 'route_not_found'),
      'GET /api/routes/3/profile': apiError(404, 'route_not_found'),
    });
    tick(ws, { distance_m: 5 });
    expect(await screen.findByText(/no longer available/i)).toBeInTheDocument();
    expect(screen.queryByTestId('route-pos')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Harder' })).toBeInTheDocument();
  });

  describe('views', () => {
    let setMedia: (k: 'wide' | 'reduced', v: boolean) => void;
    beforeEach(() => {
      setMedia = mockMedia({ wide: true, reduced: false });
    });
    const riding = async () => {
      const { ws } = await setup();
      tick(ws, { distance_m: 1 });
    };
    it('a saved 3D view renders Ride3D instead of the 2D map', async () => {
      withWebgl();
      localStorage.setItem('wattson.view', '3d');
      localStorage.setItem('wattson.view.exaggeration', '2');
      await riding();
      const r3 = await screen.findByTestId('ride3d');
      expect(r3).toHaveAttribute('data-ex', '2');
      expect(screen.queryByTestId('mapview')).not.toBeInTheDocument();
      expect(screen.getByTestId('route-pos')).toBeInTheDocument();
    });

    it('slow fallback returns to 2D, saves it and explains', async () => {
      withWebgl();
      localStorage.setItem('wattson.view', '3d');
      await riding();
      await userEvent.click(await screen.findByRole('button', { name: 'mock slow' }));
      await screen.findByTestId('mapview');
      expect(screen.queryByTestId('ride3d')).not.toBeInTheDocument();
      expect(localStorage.getItem('wattson.view')).toBe('2d');
      expect(
        screen.getByText('3D was too slow on this display, so the ride switched to 2D. Choose 3D in View to try again.'),
      ).toBeInTheDocument();
    });

    it('a lost context says so', async () => {
      withWebgl();
      localStorage.setItem('wattson.view', '3d');
      await riding();
      await userEvent.click(await screen.findByRole('button', { name: 'mock lost' }));
      expect(await screen.findByText('The 3D view stopped responding, so the ride switched to 2D.')).toBeInTheDocument();
    });

    it('choosing 3D in the menu resets the tier and passes it on', async () => {
      withWebgl();
      vi.spyOn(navigator, 'hardwareConcurrency', 'get').mockReturnValue(16);
      localStorage.setItem('wattson.view.tier', 'lite');
      await riding();
      await screen.findByTestId('mapview');
      await userEvent.click(screen.getByRole('button', { name: 'View' }));
      await userEvent.click(screen.getByRole('radio', { name: /3D flyover/ }));
      await screen.findByTestId('ride3d');
      expect(localStorage.getItem('wattson.view')).toBe('3d');
      expect(localStorage.getItem('wattson.view.tier')).toBe('full');
      expect(screen.getByTestId('ride3d')).toHaveAttribute('data-tier', 'full');
    });

    it('Escape with the View menu open closes it and does not go back', async () => {
      const back = vi.spyOn(history, 'back').mockImplementation(() => {});
      await riding();
      await screen.findByTestId('mapview');
      const view = screen.getByRole('button', { name: 'View' });
      await userEvent.click(view);
      expect(screen.getByRole('radiogroup')).toBeInTheDocument();
      await userEvent.keyboard('{Escape}');
      expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument();
      expect(view).toHaveFocus();
      expect(back).not.toHaveBeenCalled();
      await userEvent.keyboard('{Escape}');
      expect(back).toHaveBeenCalledTimes(1);
    });

    it('3D stays unmounted while the phone map panel is hidden', async () => {
      withWebgl();
      setMedia('wide', false);
      localStorage.setItem('wattson.view', '3d');
      await riding();
      await screen.findByRole('button', { name: 'Map' });
      expect(screen.queryByTestId('ride3d')).not.toBeInTheDocument();
      await userEvent.click(screen.getByRole('button', { name: 'Map' }));
      expect(await screen.findByTestId('ride3d')).toBeInTheDocument();
      await userEvent.click(screen.getByRole('button', { name: 'Hide map' }));
      expect(screen.queryByTestId('ride3d')).not.toBeInTheDocument();
    });

    it('reduced motion turned on mid-ride unmounts 3D', async () => {
      withWebgl();
      localStorage.setItem('wattson.view', '3d');
      await riding();
      await screen.findByTestId('ride3d');
      setMedia('reduced', true);
      await screen.findByTestId('mapview');
      expect(screen.queryByTestId('ride3d')).not.toBeInTheDocument();
    });

    it('the WebGL probe releases its context', async () => {
      const lose = vi.fn();
      vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(((type: string) =>
        type === 'webgl2' ? { getExtension: () => ({ loseContext: lose }) } : null) as unknown as HTMLCanvasElement['getContext']);
      await riding();
      await screen.findByTestId('mapview');
      expect(lose).toHaveBeenCalled();
    });

    it('without WebGL a saved 3D view shows the 2D map and 3D is disabled', async () => {
      localStorage.setItem('wattson.view', '3d');
      await riding();
      await screen.findByTestId('mapview');
      expect(screen.queryByTestId('ride3d')).not.toBeInTheDocument();
      await userEvent.click(screen.getByRole('button', { name: 'View' }));
      expect(screen.getByRole('radio', { name: /3D flyover/ })).toBeDisabled();
      expect(screen.getByText("3D isn't supported on this display")).toBeInTheDocument();
    });

    describe('street photos', () => {
      const photo = { id: '1', d_m: 0, bearing_deg: 0, captured_at: 0, lat: 45, lon: 7, pano: false };
      const photos = (over: Record<string, unknown>) => ({ 'GET /api/routes/3/mapillary': [200, { photos: [], ...over }] as [number, unknown] });
      const open = async () => {
        await screen.findByTestId('mapview');
        await userEvent.click(screen.getByRole('button', { name: 'View' }));
      };

      it('ready photos enable the option and a saved photo view renders PhotoView', async () => {
        localStorage.setItem('wattson.view', 'photo');
        const { ws } = await setup('/?route=3', photos({ status: 'ready', photos: [photo] }));
        tick(ws, { distance_m: 1 });
        const pv = await screen.findByTestId('photoview');
        expect(pv).toHaveAttribute('data-n', '1');
        expect(screen.queryByTestId('mapview')).not.toBeInTheDocument();
        await userEvent.click(screen.getByRole('button', { name: 'View' }));
        expect(screen.getByRole('radio', { name: 'Street photos' })).toBeEnabled();
      });

      it('building says so, then flips to photos once ready', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        localStorage.setItem('wattson.view', 'photo');
        let status = 'building';
        const { ws } = await setup('/?route=3', {
          'GET /api/routes/3/mapillary': () => [200, status === 'building' ? { status, photos: [] } : { status, photos: [photo] }],
        });
        tick(ws, { distance_m: 1 });
        await open();
        expect(screen.getByRole('radio', { name: /Street photos/ })).toBeDisabled();
        expect(screen.getAllByText('Finding street photos…').length).toBeGreaterThan(0);
        status = 'ready';
        await act(async () => void vi.advanceTimersByTime(3100)); // the 3 s poll
        await waitFor(() => expect(screen.getByTestId('photoview')).toBeInTheDocument());
      });

      it('photos + map shows both with a keyboard-reachable swap; split shows both without it', async () => {
        localStorage.setItem('wattson.view', 'photo-inset');
        const { ws } = await setup('/?route=3', photos({ status: 'ready', photos: [photo] }));
        tick(ws, { distance_m: 1 });
        await screen.findByTestId('photoview');
        expect(screen.getByTestId('mapview')).toBeInTheDocument();
        const swap = screen.getByRole('button', { name: 'Show map large' });
        expect(swap).toHaveAttribute('data-nav');
        await userEvent.click(swap);
        expect(screen.getByRole('button', { name: 'Show street photo large' })).toBeInTheDocument();
        expect(screen.getAllByTestId('mapview')).toHaveLength(1);
        await userEvent.click(screen.getByRole('button', { name: 'View' }));
        await userEvent.click(screen.getByRole('radio', { name: 'Photos | map' }));
        expect(screen.getByTestId('photoview')).toBeInTheDocument();
        expect(screen.getAllByTestId('mapview')).toHaveLength(1);
        expect(screen.queryByTestId('inset-swap')).not.toBeInTheDocument();
        expect(localStorage.getItem('wattson.view')).toBe('split');
      });

      it('a saved combined view falls back to the 2D map while photos are unavailable', async () => {
        localStorage.setItem('wattson.view', 'split');
        const { ws } = await setup('/?route=3', photos({ status: 'error', message: 'nope' }));
        tick(ws, { distance_m: 1 });
        await screen.findByTestId('mapview');
        expect(screen.queryByTestId('photoview')).not.toBeInTheDocument();
      });

      it('keeps asking while in error, and flips to photos once the server rebuilt', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        localStorage.setItem('wattson.view', 'photo');
        let status = 'error';
        const { ws } = await setup('/?route=3', {
          'GET /api/routes/3/mapillary': () => [200, status === 'error' ? { status, photos: [], message: 'boom' } : { status, photos: [photo] }],
        });
        tick(ws, { distance_m: 1 });
        await open();
        expect(screen.getByRole('radio', { name: /Street photos/ })).toBeDisabled();
        status = 'ready';
        await act(async () => void vi.advanceTimersByTime(61_000));
        await waitFor(() => expect(screen.getByTestId('photoview')).toBeInTheDocument());
      });

      it('shows the server message on error and the empty text when there is no coverage', async () => {
        const a = await setup('/?route=3', photos({ status: 'error', message: 'Mapillary rejected the token' }));
        tick(a.ws, { distance_m: 1 });
        await open();
        expect(screen.getAllByText('Mapillary rejected the token').length).toBeGreaterThan(0);
      });

      it('no coverage says so', async () => {
        const { ws } = await setup('/?route=3', photos({ status: 'ready', photos: [] }));
        tick(ws, { distance_m: 1 });
        await open();
        expect(screen.getAllByText('No street photos along this route').length).toBeGreaterThan(0);
      });
    });

    it('a synthetic route has no View button', async () => {
      const { ws } = await setup('/?route=3', {
        'GET /api/routes/3': [200, routeFixture({ id: 3, source: 'synthetic' })],
      });
      tick(ws, { distance_m: 5 });
      await screen.findByTestId('route-pos');
      expect(screen.queryByRole('button', { name: 'View' })).not.toBeInTheDocument();
    });
  });
});
