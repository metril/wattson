import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RidersPage } from './Riders';
import { apiError, renderWithClient, stubApi } from '@/test-utils';
import type { components } from '@/lib/openapi';

type Rider = components['schemas']['RiderOut'];

const rider = (over: Partial<Rider> = {}): Rider => ({
  id: 1,
  user_id: 1,
  name: 'Ann',
  weight_kg: 70,
  bike_kg: 8,
  ftp_w: 200,
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

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('RidersPage Strava return', () => {
  it('explains a rejected connection', async () => {
    stubApi({ 'GET /api/riders': [200, [rider()]] });
    renderWithClient(<RidersPage />, '/riders?strava=error&reason=scope');
    expect(
      await screen.findByText(
        'Strava was connected without upload permission. Connect again and keep “Upload your activities” ticked.',
      ),
    ).toBeInTheDocument();
  });

  it('confirms a connection', async () => {
    stubApi({ 'GET /api/riders': [200, [rider()]] });
    renderWithClient(<RidersPage />, '/riders?strava=connected');
    expect(await screen.findByText('Strava connected.')).toBeInTheDocument();
  });
});

describe('RidersPage', () => {
  it('PATCHes only the changed field', async () => {
    const stub = stubApi({
      'GET /api/riders': [200, [rider()]],
      'PATCH /api/riders/1': [200, rider({ weight_kg: 82 })],
    });
    renderWithClient(<RidersPage />);
    await userEvent.click(await screen.findByRole('button', { name: 'Edit Ann' }));
    const w = screen.getByLabelText(/Rider weight \(kg\)/);
    await userEvent.clear(w);
    await userEvent.type(w, '82');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(stub.bodies('PATCH /api/riders/1')).toEqual([{ weight_kg: 82 }]));
  });

  it('disables Delete on the only rider', async () => {
    stubApi({ 'GET /api/riders': [200, [rider()]] });
    renderWithClient(<RidersPage />);
    expect(await screen.findByRole('button', { name: 'Delete Ann' })).toBeDisabled();
  });

  it('shows a server field error under the field', async () => {
    stubApi({
      'GET /api/riders': [200, [rider()]],
      'PATCH /api/riders/1': apiError(400, 'invalid_request', 'body.weight_kg: expected number >= 30'),
    });
    renderWithClient(<RidersPage />);
    await userEvent.click(await screen.findByRole('button', { name: 'Edit Ann' }));
    const w = screen.getByLabelText(/Rider weight \(kg\)/);
    await userEvent.clear(w);
    await userEvent.type(w, '5');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    const msg = await screen.findByText('expected number >= 30');
    expect(w).toHaveAccessibleDescription(expect.stringContaining('expected number >= 30'));
    expect(msg).toBeInTheDocument();
  });

  it('shows the offset_level error under that field', async () => {
    stubApi({
      'GET /api/riders': [200, [rider()]],
      'PATCH /api/riders/1': apiError(400, 'invalid_request', 'offset_level: must be between min_level and max_level'),
    });
    renderWithClient(<RidersPage />);
    await userEvent.click(await screen.findByRole('button', { name: 'Edit Ann' }));
    const o = screen.getByLabelText(/Flat-road level/);
    await userEvent.clear(o);
    await userEvent.type(o, '30');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('must be between min_level and max_level')).toBeInTheDocument();
    expect(o).toHaveAccessibleDescription(expect.stringContaining('must be between'));
  });

  it('tells the user to release the bike when the rider is in use', async () => {
    stubApi({
      'GET /api/riders': [200, [rider(), rider({ id: 2, name: 'Bo' })]],
      'DELETE /api/riders/2': apiError(409, 'rider_in_use', 'bound'),
    });
    renderWithClient(<RidersPage />);
    await userEvent.click(await screen.findByRole('button', { name: 'Delete Bo' }));
    await userEvent.click(screen.getByRole('button', { name: 'Click again to confirm' }));
    expect(await screen.findByText('Release the bike first')).toBeInTheDocument();
  });

  it('creates a rider with the shown defaults', async () => {
    const stub = stubApi({
      'GET /api/riders': [200, [rider()]],
      'POST /api/riders': [201, rider({ id: 3, name: 'Cy' })],
    });
    renderWithClient(<RidersPage />);
    await userEvent.click(await screen.findByRole('button', { name: 'New rider' }));
    expect(screen.getByLabelText(/Rider weight \(kg\)/)).toHaveValue(75);
    expect(screen.getByLabelText(/FTP \(W\)/)).toHaveValue(200);
    await userEvent.type(screen.getByLabelText('Name'), 'Cy');
    await userEvent.click(screen.getByRole('button', { name: 'Create rider' }));
    await waitFor(() => expect(stub.bodies('POST /api/riders')).toHaveLength(1));
    expect(stub.bodies('POST /api/riders')[0]).toMatchObject({ name: 'Cy', weight_kg: 75, min_level: 1, max_level: 16 });
  });

  it('remembers the selected rider per device', async () => {
    stubApi({ 'GET /api/riders': [200, [rider(), rider({ id: 2, name: 'Bo' })]] });
    renderWithClient(<RidersPage />);
    await userEvent.click(await screen.findByRole('button', { name: 'Use Bo' }));
    expect(localStorage.getItem('wattson.rider_id')).toBe('2');
    expect(screen.getByRole('button', { name: 'Using Bo' })).toBeDisabled();
  });
});
