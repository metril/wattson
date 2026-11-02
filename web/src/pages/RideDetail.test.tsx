import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Route, Routes, useLocation } from 'react-router';
import { RideDetailPage } from './RideDetail';
import { apiError, renderWithClient, stubApi } from '@/test-utils';
import { rideDetailFixture } from '@/test-rides';

const { setLine, fit } = vi.hoisted(() => ({ setLine: vi.fn(), fit: vi.fn() }));
vi.mock('@/features/map/MapView', async () => {
  const { useEffect } = await import('react');
  const api = { setLine, fit, setMarker: () => {} };
  return {
    STYLE_URL: 'x',
    default: (p: { 'data-testid'?: string; onReady(a: unknown): void }) => {
      useEffect(() => {
        p.onReady(api);
      }, []); // eslint-disable-line react-hooks/exhaustive-deps
      return <div data-testid={p['data-testid']} />;
    },
  };
});

const Where = () => <p data-testid="where">{useLocation().pathname}</p>;
const ui = (
  <Routes>
    <Route path="/history/:id" element={<RideDetailPage />} />
    <Route path="*" element={<Where />} />
  </Routes>
);

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  localStorage.clear();
});

describe('RideDetailPage', () => {
  it('a free ride has no map and no elevation chart', async () => {
    stubApi({ 'GET /api/rides/7': [200, rideDetailFixture()] });
    renderWithClient(ui, '/history/7');
    expect(await screen.findByRole('heading', { name: 'Free ride' })).toBeInTheDocument();
    expect(screen.queryByTestId('ride-map')).not.toBeInTheDocument();
    expect(screen.queryByText('Elevation')).not.toBeInTheDocument();
    expect(screen.getByText('Power')).toBeInTheDocument();
    expect(screen.getByText('Speed')).toBeInTheDocument();
    expect(screen.getByText('Cadence')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Download FIT' })).toHaveAttribute('href', '/api/rides/7/fit');
  });

  it('a route ride draws its trace and elevation', async () => {
    stubApi({
      'GET /api/rides/7': [
        200,
        rideDetailFixture(
          { route_name: 'Ramp', route_id: 3 },
          { lon: [7, 7.1, 7.2], lat: [45, 45.1, 45.2], ele_m: [100, 110, 120] },
        ),
      ],
    });
    renderWithClient(ui, '/history/7');
    expect(await screen.findByRole('heading', { name: 'Ramp' })).toBeInTheDocument();
    expect(screen.getByText('Elevation')).toBeInTheDocument();
    await waitFor(() => expect(setLine).toHaveBeenCalled());
    const line = setLine.mock.calls[0][0];
    expect(line.type).toBe('LineString');
    expect(line.coordinates[0]).toEqual([7, 45]);
    expect(fit).toHaveBeenCalledWith([7, 45, 7.2, 45.2]);
  });

  it('hides the heart-rate chart when none was recorded and shows it otherwise', async () => {
    stubApi({ 'GET /api/rides/7': [200, rideDetailFixture()] });
    const a = renderWithClient(ui, '/history/7');
    await screen.findByText('Power');
    expect(screen.queryByText('Heart rate')).not.toBeInTheDocument();
    a.unmount();
    vi.unstubAllGlobals();
    stubApi({ 'GET /api/rides/7': [200, rideDetailFixture({}, { hr_bpm: [120, 130, 140] })] });
    renderWithClient(ui, '/history/7');
    expect(await screen.findByText('Heart rate')).toBeInTheDocument();
  });

  it('needs two taps to delete, then returns to the list', async () => {
    const api = stubApi({
      'GET /api/rides/7': [200, rideDetailFixture()],
      'DELETE /api/rides/7': [204],
    });
    renderWithClient(ui, '/history/7');
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    expect(api.bodies('DELETE /api/rides/7')).toHaveLength(0);
    await userEvent.click(screen.getByRole('button', { name: 'Tap again to delete' }));
    expect(await screen.findByTestId('where')).toHaveTextContent('/history');
    expect(api.calls.filter((c) => c.key === 'DELETE /api/rides/7')).toHaveLength(1);
  });

  it('explains a ride that is still recording', async () => {
    stubApi({
      'GET /api/rides/7': [200, rideDetailFixture()],
      'DELETE /api/rides/7': apiError(409, 'ride_in_progress'),
    });
    renderWithClient(ui, '/history/7');
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    await userEvent.click(screen.getByRole('button', { name: 'Tap again to delete' }));
    expect(await screen.findByText('This ride is still being recorded.')).toBeInTheDocument();
  });

  it('says so when the ride is gone', async () => {
    stubApi({ 'GET /api/rides/7': apiError(404, 'not_found') });
    renderWithClient(ui, '/history/7');
    expect(await screen.findByText(/ride no longer exists/i)).toBeInTheDocument();
  });
});
