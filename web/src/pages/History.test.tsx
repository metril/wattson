import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { HistoryPage } from './History';
import { renderWithClient, stubApi } from '@/test-utils';
import { rideFixture, stravaNone } from '@/test-rides';
import type { components } from '@/lib/openapi';

type Rider = components['schemas']['RiderOut'];
const rider = (id: number, name: string): Rider =>
  ({ id, user_id: 1, name, weight_kg: 70, ftp_w: 200, min_level: 1, max_level: 16 }) as Rider;
const riders = [rider(1, 'Ann'), rider(2, 'Bob')];

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

const urls = () =>
  (fetch as unknown as { mock: { calls: [Request][] } }).mock.calls.map(([r]) => {
    const u = new URL(r.url);
    return u.pathname + u.search;
  });

describe('HistoryPage', () => {
  it('lists a ride with its figures and links to it', async () => {
    stubApi({
      'GET /api/riders': [200, riders],
      'GET /api/rides': [
        200,
        {
          rides: [
            rideFixture({
              id: 9,
              route_name: 'Ramp',
              distance_m: 1200,
              duration_s: 120,
              np_w: 318.35,
              kj: 24,
              avg_power_w: 301,
              strava: { ...stravaNone, status: 'done' },
            }),
          ],
          next_before: 0,
        },
      ],
    });
    renderWithClient(<HistoryPage />, '/history');
    expect(await screen.findByText('Ramp')).toBeInTheDocument();
    const row = screen.getByRole('link', { name: /Ramp/ });
    for (const t of ['1.20', '00:02:00', '318', '24', '301', 'Strava']) expect(within(row).getByText(t)).toBeInTheDocument();
    expect(row).toHaveAttribute('href', '/history/9');
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();
  });

  it('shows Free ride for a ride without a route', async () => {
    stubApi({ 'GET /api/riders': [200, riders], 'GET /api/rides': [200, { rides: [rideFixture()], next_before: 0 }] });
    renderWithClient(<HistoryPage />, '/history');
    expect(await screen.findByText('Free ride')).toBeInTheDocument();
  });

  it('pages with before=next_before', async () => {
    let calls = 0;
    stubApi({
      'GET /api/riders': [200, riders],
      'GET /api/rides': (): [number, unknown] => {
        // The 30-day summary asks for 100 rides; only list pages count.
        if (urls().at(-1)?.includes('limit=100')) return [200, { rides: [], next_before: 0 }];
        return [200, { rides: [rideFixture({ id: 6 - calls })], next_before: calls++ === 0 ? 5 : 0 }];
      },
    });
    renderWithClient(<HistoryPage />, '/history');
    await userEvent.click(await screen.findByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(urls()).toContain('/api/rides?before=5&limit=20'));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument());
  });

  it('filters by rider and remembers the choice', async () => {
    stubApi({
      'GET /api/riders': [200, riders],
      'GET /api/rides': [200, { rides: [rideFixture()], next_before: 0 }],
    });
    renderWithClient(<HistoryPage />, '/history');
    await userEvent.selectOptions(await screen.findByLabelText('Rider'), 'Bob');
    await waitFor(() => expect(urls().some((u) => u.includes('rider_id=2'))).toBe(true));
    expect(localStorage.getItem('wattson.history_rider')).toBe('2');
  });

  it('explains an empty history', async () => {
    stubApi({ 'GET /api/riders': [200, riders], 'GET /api/rides': [200, { rides: [], next_before: 0 }] });
    renderWithClient(<HistoryPage />, '/history');
    expect(await screen.findByText('No rides yet. Finished rides appear here.')).toBeInTheDocument();
  });
});
