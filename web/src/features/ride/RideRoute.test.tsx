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
});
