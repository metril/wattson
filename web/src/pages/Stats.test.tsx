import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { StatsPage } from './Stats';
import { renderWithClient, stubApi } from '@/test-utils';

afterEach(() => vi.unstubAllGlobals());

const period = (start: string, rides: number) => ({
  start, rides, duration_s: 3600 * rides, distance_m: 20000 * rides, ascent_m: 100 * rides, kj: 500 * rides, tss: 50 * rides,
});

const stubs = () =>
  stubApi({
    'GET /api/riders': [200, [{ id: 1, name: 'Ann' }, { id: 2, name: 'Bob' }]],
    'GET /api/stats/totals': [
      200,
      {
        period: 'week',
        tz: 'UTC',
        periods: [period('2026-05-04', 0), period('2026-05-11', 2)],
        streaks: { current_days: 3, longest_days: 5, current_weeks: 2, longest_weeks: 4 },
      },
    ],
    'GET /api/stats/fitness': [200, { tz: 'UTC', days: [{ date: '2026-05-11', tss: 50, ctl: 5, atl: 20, tsb: -15 }] }],
    'GET /api/stats/power-curve': [
      200,
      { window: 'all', eftp_w: 237.5, points: [{ dur_s: 5, watts: 800, w_per_kg: 10, ride_id: 1, date: '2026-05-01' }] },
    ],
  });

const urls = () =>
  (fetch as unknown as { mock: { calls: [Request][] } }).mock.calls.map(([r]) => {
    const u = new URL(r.url);
    return u.pathname + u.search;
  });

describe('StatsPage', () => {
  it('shows streaks, this period, eFTP and the charts', async () => {
    stubs();
    renderWithClient(<StatsPage />);
    expect(await screen.findByText('3 days')).toBeInTheDocument();
    expect(screen.getByText('4 weeks')).toBeInTheDocument();
    expect(screen.getByLabelText('This week')).toHaveTextContent('40.00 km');
    expect(screen.getByLabelText('This week')).toHaveTextContent('2:00:00');
    expect(await screen.findByTestId('eftp')).toHaveTextContent('eFTP 238 W');
    expect(await screen.findByTestId('form-card')).toHaveTextContent('Fatigued');
    expect(screen.getByRole('img', { name: /distance per week/i })).toBeInTheDocument();
    expect(await screen.findByRole('img', { name: /fitness, fatigue and form/i })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: /best power from 5 s/i })).toBeInTheDocument();
  });

  it('sends the browser zone and refetches for the chosen period and rider', async () => {
    stubs();
    renderWithClient(<StatsPage />);
    await screen.findByText('3 days');
    expect(urls().some((u) => u.startsWith('/api/stats/totals?') && u.includes('period=week') && u.includes('tz='))).toBe(true);
    await userEvent.click(screen.getByRole('radio', { name: 'Yearly' }));
    await userEvent.selectOptions(await screen.findByLabelText('Rider'), '2');
    await waitFor(() => expect(urls().some((u) => u.includes('period=year') && u.includes('rider_id=2'))).toBe(true));
    await waitFor(() => expect(urls().some((u) => u.startsWith('/api/stats/power-curve') && u.includes('rider_id=2'))).toBe(true));
  });

  it('explains an empty power curve and fitness', async () => {
    stubApi({
      'GET /api/riders': [200, [{ id: 1, name: 'Ann' }]],
      'GET /api/stats/totals': [200, { period: 'week', tz: 'UTC', periods: [period('2026-05-11', 0)], streaks: { current_days: 0, longest_days: 0, current_weeks: 0, longest_weeks: 0 } }],
      'GET /api/stats/fitness': [200, { tz: 'UTC', days: [] }],
      'GET /api/stats/power-curve': [200, { window: 'all', points: [], eftp_w: null }],
    });
    renderWithClient(<StatsPage />);
    expect(await screen.findByText(/No power data in this window yet/)).toBeInTheDocument();
    expect(screen.getByText(/Fitness appears after your first ride/)).toBeInTheDocument();
    expect(screen.queryByTestId('eftp')).not.toBeInTheDocument();
  });
});
