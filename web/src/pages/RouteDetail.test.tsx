import { fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Route, Routes, useLocation } from 'react-router';
import { RouteDetailPage } from './RouteDetail';
import { apiError, profileFixture, renderWithClient, routeFixture, stubApi, userFixture } from '@/test-utils';
import type { components } from '@/lib/openapi';

vi.mock('@/features/map/MapView', () => ({
  STYLE_URL: 'x',
  default: (p: { 'data-testid'?: string }) => <div data-testid={p['data-testid']} />,
}));

type Rider = components['schemas']['RiderOut'];
const rider = (over: Partial<Rider> = {}): Rider => ({
  id: 1,
  user_id: 1,
  name: 'Ann',
  weight_kg: 70,
  bike_kg: 8,
  ftp_w: 200,
  max_hr_bpm: null,
  cda: 0.32,
  crr: 0.004,
  power_multiplier: 1,
  min_level: 1,
  max_level: 16,
  offset_level: 5,
  gain: 1,
  downhill_scale: 0.5,
  lead_s: 4,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  ...over,
});

const me = (over = {}) => [200, { user: userFixture({ id: 1, is_admin: false, ...over }), scopes: [], via: 'session' }] as [number, unknown];

const Where = () => {
  const l = useLocation();
  return <p data-testid="where">{l.pathname + l.search}</p>;
};
const ui = (
  <Routes>
    <Route path="/routes/:id" element={<RouteDetailPage />} />
    <Route path="*" element={<Where />} />
  </Routes>
);

function base(over: Parameters<typeof stubApi>[0] = {}) {
  return stubApi({
    'GET /api/auth/me': me(),
    'GET /api/routes/3': [200, routeFixture({ id: 3 })],
    'GET /api/routes/3/profile': [200, profileFixture({ route_id: 3 })],
    'GET /api/riders': [200, [rider()]],
    ...over,
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('RouteDetailPage', () => {
  it('shows stats and a map for a drawn or imported route', async () => {
    base();
    renderWithClient(ui, '/routes/3');
    expect(await screen.findByRole('heading', { name: 'Ramp' })).toBeInTheDocument();
    expect(screen.getByTestId('route-map')).toBeInTheDocument();
    expect(screen.getByText('1.00')).toBeInTheDocument();
  });

  it('renders no map for a synthetic route', async () => {
    base({ 'GET /api/routes/3': [200, routeFixture({ id: 3, source: 'synthetic' })] });
    renderWithClient(ui, '/routes/3');
    await screen.findByRole('heading', { name: 'Ramp' });
    expect(screen.queryByTestId('route-map')).not.toBeInTheDocument();
  });

  it('previews the rider levels on a 6 % profile', async () => {
    base();
    renderWithClient(ui, '/routes/3');
    expect(await screen.findByText('max L11')).toBeInTheDocument();
    expect(screen.getByText('min L11')).toBeInTheDocument();
  });

  it("bounds the flat-road slider to the rider's levels", async () => {
    base({ 'GET /api/riders': [200, [rider({ min_level: 3, max_level: 12 })]] });
    renderWithClient(ui, '/routes/3');
    const s = await screen.findByLabelText(/^Flat-road level/);
    expect(s).toHaveAttribute('min', '3');
    expect(s).toHaveAttribute('max', '12');
  });

  it('saves only the changed slider to the rider', async () => {
    const stub = base({ 'PATCH /api/riders/1': [200, rider({ gain: 2 })] });
    renderWithClient(ui, '/routes/3');
    const save = await screen.findByRole('button', { name: 'Save to rider' });
    expect(save).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/^Gain/), { target: { value: '2' } });
    expect(screen.getByText('max L16')).toBeInTheDocument(); // 5 + 6*2 clamps to 16
    await userEvent.click(save);
    await waitFor(() => expect(stub.bodies('PATCH /api/riders/1')).toEqual([{ gain: 2 }]));
  });

  it('remembers the chosen rider', async () => {
    base({ 'GET /api/riders': [200, [rider(), rider({ id: 2, name: 'Bo' })]] });
    renderWithClient(ui, '/routes/3');
    await userEvent.selectOptions(await screen.findByLabelText('Rider'), 'Bo');
    expect(localStorage.getItem('wattson.rider_id')).toBe('2');
  });

  it('links to the ride screen', async () => {
    base();
    renderWithClient(ui, '/routes/3');
    const link = await screen.findByRole('link', { name: /Ride this route/ });
    expect(link).toHaveAttribute('href', '/?route=3');
    await userEvent.click(link);
    expect(screen.getByTestId('where')).toHaveTextContent('/?route=3');
  });

  it('deletes on the second tap and returns to the list', async () => {
    const stub = base({ 'DELETE /api/routes/3': [204] });
    renderWithClient(ui, '/routes/3');
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    expect(stub.calls.some((c) => c.key === 'DELETE /api/routes/3')).toBe(false);
    await userEvent.click(screen.getByRole('button', { name: 'Tap again to delete' }));
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/routes'));
  });

  it('renames for the owner and hides edit controls from others', async () => {
    const stub = base({ 'PATCH /api/routes/3': [200, routeFixture({ id: 3, name: 'Hill' })] });
    renderWithClient(ui, '/routes/3');
    await userEvent.click(await screen.findByRole('button', { name: 'Rename route' }));
    const f = screen.getByLabelText('Route name');
    await userEvent.clear(f);
    await userEvent.type(f, 'Hill');
    await userEvent.click(screen.getByRole('button', { name: 'Save name' }));
    await waitFor(() => expect(stub.bodies('PATCH /api/routes/3')).toEqual([{ name: 'Hill' }]));
  });

  it('hides rename and delete for a non-owner', async () => {
    base({ 'GET /api/auth/me': me({ id: 9 }) });
    renderWithClient(ui, '/routes/3');
    await screen.findByRole('heading', { name: 'Ramp' });
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Rename route' })).not.toBeInTheDocument();
  });

  it('shows a calm message when the route is gone', async () => {
    base({ 'GET /api/routes/3': apiError(404, 'not_found'), 'GET /api/routes/3/profile': apiError(404, 'not_found') });
    renderWithClient(ui, '/routes/3');
    expect(await screen.findByText(/no longer exists/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Back to routes' })).toHaveAttribute('href', '/routes');
  });

  it('keeps the page when only the profile is gone', async () => {
    base({ 'GET /api/routes/3/profile': apiError(404, 'not_found') });
    renderWithClient(ui, '/routes/3');
    expect(await screen.findByText(/no elevation profile/)).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Ramp' })).toBeInTheDocument();
  });
});
