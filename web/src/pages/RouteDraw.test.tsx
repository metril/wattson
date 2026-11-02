import { act, cleanup, fireEvent, screen } from '@testing-library/react';
import { Route, Routes, useLocation } from 'react-router';
import { RouteDrawPage } from './RouteDraw';
import { apiError, profileFixture, renderWithClient, routeFixture, stubApi, userFixture } from '@/test-utils';

const m = vi.hoisted(() => ({
  props: null as null | { onClick?(ll: [number, number]): void; center?: [number, number]; zoom?: number },
  api: { getCenter: vi.fn(() => [8.5, 46.25] as [number, number]), setLine: vi.fn(), fit: vi.fn(), setMarker: vi.fn(), setWaypoints: vi.fn(), setTiles: vi.fn() },
}));

vi.mock('@/features/map/MapView', async () => {
  const { useEffect } = await import('react');
  return {
    STYLE_URL: 'x',
    default: (p: NonNullable<typeof m.props> & { onReady(a: unknown): void }) => {
      m.props = p;
      useEffect(() => p.onReady(m.api), []); // eslint-disable-line react-hooks/exhaustive-deps
      return <div data-testid="draw-map" />;
    },
  };
});

const A: [number, number] = [7.5, 45.5];
const B: [number, number] = [7.51, 45.51];
const click = (ll: [number, number]) => act(() => m.props?.onClick?.(ll));
const settle = (ms = 400) => act(() => vi.advanceTimersByTimeAsync(ms));

const me = (admin: boolean) =>
  [200, { user: userFixture({ id: 1, is_admin: admin }), scopes: [], via: 'session' }] as [number, unknown];
const drawn = (id = 0) => [
  200,
  { route: routeFixture({ id, distance_m: 12345 }), profile: profileFixture({ route_id: id }) },
] as [number, unknown];

const Where = () => <p data-testid="where">{useLocation().pathname}</p>;
const ui = (
  <Routes>
    <Route path="/routes/new/draw" element={<RouteDrawPage />} />
    <Route path="*" element={<Where />} />
  </Routes>
);
const DRAW = 'POST /api/routes/draw';
const draws = (s: ReturnType<typeof stubApi>) => s.bodies(DRAW);

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: false });
  m.props = null;
  Object.values(m.api).forEach((f) => f.mockClear());
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

async function render(stubs: Parameters<typeof stubApi>[0] = {}, route = '/routes/new/draw', admin = true) {
  const s = stubApi({ 'GET /api/auth/me': me(admin), [DRAW]: drawn(), ...stubs });
  renderWithClient(ui, route);
  await settle(0);
  return s;
}

describe('RouteDrawPage', () => {
  it('requests a dry-run preview after the second waypoint and 400 ms', async () => {
    const s = await render();
    click(A);
    await settle(1000);
    expect(draws(s)).toHaveLength(0);
    click(B);
    await settle(399);
    expect(draws(s)).toHaveLength(0);
    await settle(1);
    expect(draws(s)).toEqual([{ waypoints: [A, B], dry_run: true }]);
    await settle(0);
    expect(screen.getByText('12.35')).toBeInTheDocument();
    expect(m.api.setLine).toHaveBeenLastCalledWith(expect.objectContaining({ type: 'LineString' }));
  });

  it('debounces rapid clicks into one request', async () => {
    const s = await render();
    click(A);
    await settle(100);
    click(B);
    await settle(100);
    click([7.52, 45.52]);
    await settle(399);
    expect(draws(s)).toHaveLength(0);
    await settle(1);
    expect(draws(s)).toHaveLength(1);
    expect((draws(s)[0] as { waypoints: unknown[] }).waypoints).toHaveLength(3);
  });

  it('ignores a superseded in-flight response', async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    let n = 0;
    await render({
      [DRAW]: async () => {
        if (n++ === 0) {
          await gate;
          return [200, { route: routeFixture({ distance_m: 111000 }), profile: profileFixture({ route_id: 0 }) }];
        }
        return [200, { route: routeFixture({ distance_m: 222000 }), profile: profileFixture({ route_id: 0 }) }];
      },
    });
    click(A);
    click(B);
    await settle();
    click([7.52, 45.52]);
    await settle();
    release();
    await settle(0);
    expect(screen.getByText('222.00')).toBeInTheDocument();
    expect(screen.queryByText('111.00')).not.toBeInTheDocument();
  });

  it('undo drops the last waypoint without a new request', async () => {
    const s = await render();
    click(A);
    click(B);
    await settle();
    expect(draws(s)).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: /undo/i }));
    await settle(1000);
    expect(draws(s)).toHaveLength(1);
    expect(screen.getByText('1 / 30 waypoints')).toBeInTheDocument();
    expect(m.api.setLine).toHaveBeenLastCalledWith(null);
  });

  it('removes a waypoint from the list and clears them all', async () => {
    await render();
    click(A);
    click(B);
    fireEvent.click(screen.getByRole('button', { name: 'Remove waypoint 1' }));
    expect(screen.getByText('1 / 30 waypoints')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /clear/i }));
    expect(screen.getByText('0 / 30 waypoints')).toBeInTheDocument();
  });

  it('ignores clicks beyond 30 waypoints', async () => {
    await render();
    for (let i = 0; i < 32; i++) click([7 + i / 100, 45]);
    expect(screen.getByText('30 / 30 waypoints')).toBeInTheDocument();
  });

  it('sends the chosen profile', async () => {
    const s = await render();
    fireEvent.change(screen.getByLabelText('Routing profile'), { target: { value: 'safety' } });
    click(A);
    click(B);
    await settle();
    expect(draws(s)[0]).toMatchObject({ profile: 'safety' });
  });

  it('leaves the profile to the server unless one is picked, for everyone', async () => {
    for (const admin of [true, false]) {
      const s = await render({}, '/routes/new/draw', admin);
      expect(screen.getByLabelText('Routing profile')).toHaveValue('');
      click(A);
      click(B);
      await settle();
      expect(draws(s)[0]).not.toHaveProperty('profile');
      cleanup();
      vi.unstubAllGlobals();
    }
  });

  it('adds a waypoint at the map centre from a button', async () => {
    const s = await render();
    fireEvent.click(screen.getByRole('button', { name: 'Add waypoint at map centre' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add waypoint at map centre' }));
    await settle();
    expect(screen.getByText('2 / 30 waypoints')).toBeInTheDocument();
    expect(draws(s)[0]).toMatchObject({ waypoints: [[8.5, 46.25], [8.5, 46.25]] });
  });

  it('disables Save while the preview is out of date', async () => {
    await render();
    click(A);
    click(B);
    await settle();
    expect(screen.getByRole('button', { name: 'Save route' })).toBeEnabled();
    click([7.52, 45.52]);
    expect(screen.getByRole('button', { name: 'Save route' })).toBeDisabled();
    await settle();
    expect(screen.getByRole('button', { name: 'Save route' })).toBeEnabled();
  });

  it('missing map regions: admins get a link and a download button', async () => {
    const s = await render({
      [DRAW]: apiError(422, 'missing_tiles', 'missing map regions: E5_N45. Download them in Settings.', {
        missing_tiles: ['E5_N45'],
      }),
      'POST /api/maps/tiles/E5_N45': [202, {}],
    });
    click(A);
    click(B);
    await settle();
    await settle(0);
    const banner = screen.getByText(/Map data missing for E5_N45/);
    expect(banner).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Map regions' })).toHaveAttribute('href', '/settings/maps');
    fireEvent.click(screen.getByRole('button', { name: 'Download these regions' }));
    await settle(0);
    expect(s.calls.some((c) => c.key === 'POST /api/maps/tiles/E5_N45')).toBe(true);
    expect(screen.getByText(/Download started/)).toBeInTheDocument();
  });

  it('missing map regions: others are told to ask an admin', async () => {
    await render(
      {
        [DRAW]: apiError(422, 'missing_tiles', 'missing map regions: E5_N45', { missing_tiles: ['E5_N45'] }),
      },
      '/routes/new/draw',
      false,
    );
    click(A);
    click(B);
    await settle();
    await settle(0);
    expect(screen.getByText(/Map data missing for E5_N45/)).toHaveTextContent(/ask an admin/);
    expect(screen.queryByRole('link', { name: 'Map regions' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Download these regions' })).not.toBeInTheDocument();
  });

  it('shows the server message on 502', async () => {
    await render({ [DRAW]: apiError(502, 'brouter_unavailable', 'BRouter did not answer') });
    click(A);
    click(B);
    await settle();
    await settle(0);
    expect(screen.getByRole('alert')).toHaveTextContent('BRouter did not answer');
  });

  it('saves with the name and opens the route', async () => {
    const s = await render({ [DRAW]: (b: { dry_run?: boolean }) => (b.dry_run ? drawn() : drawn(9)) });
    click(A);
    click(B);
    await settle();
    await settle(0);
    fireEvent.change(screen.getByLabelText('Route name'), { target: { value: 'Loop' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save route' }));
    await settle(0);
    expect(draws(s)[1]).toEqual({ waypoints: [A, B], dry_run: false, name: 'Loop' });
    expect(screen.getByTestId('where')).toHaveTextContent('/routes/9');
  });

  it('starts at ?c=lon,lat&z=zoom', async () => {
    await render({}, '/routes/new/draw?c=8.5,46.5&z=9');
    expect(m.props?.center).toEqual([8.5, 46.5]);
    expect(m.props?.zoom).toBe(9);
  });

  it('defaults to the newest route, else the Alps', async () => {
    await render({
      'GET /api/routes': [200, [routeFixture({ id: 4 })]],
      'GET /api/routes/4': [200, routeFixture({ id: 4, bbox: [10, 50, 11, 51] })],
    });
    await settle(0);
    expect(m.api.fit).toHaveBeenCalledWith([10, 50, 11, 51]);
    expect(m.props?.center).toEqual([7.5, 45.5]);
    expect(m.props?.zoom).toBe(5);
  });
});
