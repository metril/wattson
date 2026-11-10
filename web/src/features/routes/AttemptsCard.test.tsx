import { screen, waitFor } from '@testing-library/react';
import { AttemptsCard } from './AttemptsCard';
import { render } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { stubApi } from '@/test-utils';

const at = (id: number, completed: boolean) => ({
  ride_id: id, started_at: '2026-05-01T08:00:00Z', elapsed_s: 100 + id, avg_speed_kmh: 30,
  np_w: 200, avg_power_w: 190, avg_hr_bpm: 0, completed, is_pr: false,
});
const stats = (list: ReturnType<typeof at>[]) => ({
  attempts: list.length, completed: list.filter((a) => a.completed).length, best_s: 101, avg_s: 102, last_s: 102,
  best_speed_kmh: 30, pr_date: null, best_np_w: 200, avg_power_w: 190, avg_hr_bpm: 0, list,
});

describe('AttemptsCard trend', () => {
  it('draws the trend once a second completed attempt arrives', async () => {
    let body = stats([at(1, true)]);
    stubApi({ 'GET /api/routes/7/stats': () => [200, body] });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <AttemptsCard routeId={7} units="metric" />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await screen.findByText('Your rides');
    expect(screen.queryByRole('img', { name: /Completed times/ })).toBeNull();
    body = stats([at(1, true), at(2, true)]);
    await client.invalidateQueries();
    await waitFor(() => expect(screen.getByRole('img', { name: /Completed times/ })).toBeInTheDocument());
  });
});
