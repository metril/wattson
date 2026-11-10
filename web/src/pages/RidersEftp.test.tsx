import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RidersPage } from './Riders';
import { renderWithClient, stubApi } from '@/test-utils';
import type { components } from '@/lib/openapi';

type Rider = components['schemas']['RiderOut'];

const rider = (): Rider => ({
  id: 1, user_id: 1, name: 'Ann', weight_kg: 70, bike_kg: 8, ftp_w: 200, max_hr_bpm: null, cda: 0.32, crr: 0.004,
  power_multiplier: 1, min_level: 1, max_level: 16, offset_level: 5, gain: 1, downhill_scale: 0.5, lead_s: 4,
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
});

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('rider form eFTP', () => {
  it('fills FTP from the 90-day eFTP without saving', async () => {
    const stub = stubApi({
      'GET /api/riders': [200, [rider()]],
      'GET /api/stats/power-curve': [200, { window: '90d', points: [], eftp_w: 237.6 }],
    });
    renderWithClient(<RidersPage />);
    await userEvent.click(await screen.findByRole('button', { name: 'Edit Ann' }));
    await userEvent.click(screen.getByRole('button', { name: 'Use eFTP' }));
    await waitFor(() => expect(screen.getByLabelText(/FTP \(W\)/)).toHaveValue(238));
    expect(screen.getByText(/Save to apply/)).toBeInTheDocument();
    expect(stub.calls.some((c) => c.key === 'PATCH /api/riders/1')).toBe(false);
  });

  it('says so when there is no 20-minute effort', async () => {
    stubApi({
      'GET /api/riders': [200, [rider()]],
      'GET /api/stats/power-curve': [200, { window: '90d', points: [], eftp_w: null }],
    });
    renderWithClient(<RidersPage />);
    await userEvent.click(await screen.findByRole('button', { name: 'Edit Ann' }));
    await userEvent.click(screen.getByRole('button', { name: 'Use eFTP' }));
    expect(await screen.findByText(/No 20-minute effort/)).toBeInTheDocument();
    expect(screen.getByLabelText(/FTP \(W\)/)).toHaveValue(200);
  });
});
