import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { components } from '@/lib/openapi';
import { apiError, renderWithClient, settingsFixture, stubApi } from '@/test-utils';
import { BikeForm } from './Bike';

const dev = { address: 'AA:BB:CC:DD:EE:FF', name: 'MRK-S29R2', protocol: 'fitplus', rssi: -60 };

function setup(bike: Partial<components['schemas']['BikeOut']> = {}, extra: Parameters<typeof stubApi>[0] = {}) {
  const s = settingsFixture({ bike: { driver: 'fitplus', ...bike } });
  return stubApi({ 'GET /api/settings': [200, s], 'PATCH /api/settings': [200, s], ...extra });
}

describe('BikeForm', () => {
  it('hides address and transport for the simulator', async () => {
    setup({ driver: 'sim' });
    renderWithClient(<BikeForm />);
    await screen.findByLabelText('Driver');
    expect(screen.queryByLabelText('Bike address')).toBeNull();
    expect(screen.queryByRole('radiogroup')).toBeNull();
    expect(screen.getByLabelText(/simulator cadence/i)).toBeInTheDocument();
  });

  it('shows ESPHome host and key when that transport is chosen', async () => {
    setup();
    renderWithClient(<BikeForm />);
    await screen.findByLabelText('Driver');
    expect(screen.queryByLabelText('ESPHome host')).toBeNull();
    await userEvent.click(screen.getByRole('radio', { name: /esphome proxy/i }));
    expect(screen.getByLabelText('ESPHome host')).toBeInTheDocument();
    expect(screen.getByLabelText('ESPHome API key')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /test esphome/i })).toBeInTheDocument();
  });

  it('scans and fills the address from a clicked row', async () => {
    const api = setup({}, { 'POST /api/bike/scan': [200, { devices: [dev] }] });
    renderWithClient(<BikeForm />);
    await userEvent.click(await screen.findByRole('button', { name: 'Scan' }));
    const row = await screen.findByRole('row', { name: /MRK-S29R2/ });
    const cells = within(row).getAllByRole('cell').map((c) => c.textContent);
    expect(cells).toEqual(['AA:BB:CC:DD:EE:FF', 'MRK-S29R2', '-60 dBm', 'fitplus']);
    expect(api.bodies('POST /api/bike/scan')[0]).toMatchObject({ duration_s: 8, transport: 'local' });
    await userEvent.click(row);
    expect(screen.getByLabelText('Bike address')).toHaveValue('AA:BB:CC:DD:EE:FF');
  });

  it('tells the user to release the bike when scan is refused', async () => {
    setup({}, { 'POST /api/bike/scan': apiError(409, 'bike_busy') });
    renderWithClient(<BikeForm />);
    await userEvent.click(await screen.findByRole('button', { name: 'Scan' }));
    expect(await screen.findByText(/Release the bike/)).toBeInTheDocument();
  });

  it('renders invalid_setting under the address field', async () => {
    setup({}, { 'PATCH /api/settings': apiError(400, 'invalid_setting', 'bike.address: not a Bluetooth address') });
    renderWithClient(<BikeForm />);
    await userEvent.type(await screen.findByLabelText('Bike address'), 'zz');
    await userEvent.click(screen.getByRole('button', { name: /save/i }));
    const err = await screen.findByText('not a Bluetooth address');
    expect(screen.getByLabelText('Bike address')).toHaveAttribute('aria-describedby', expect.stringContaining(err.id));
  });

  it('PATCHes only changed fields and calls onSaved', async () => {
    const api = setup();
    const onSaved = vi.fn();
    renderWithClient(<BikeForm onSaved={onSaved} />);
    const idle = await screen.findByLabelText(/idle auto-release/i);
    await userEvent.clear(idle);
    await userEvent.type(idle, '30');
    await userEvent.click(screen.getByRole('button', { name: /save/i }));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(api.bodies('PATCH /api/settings')[0]).toEqual({ bike: { idle_release_min: 30 } });
  });

  it('shows the release hint when saving is refused with bike_busy', async () => {
    setup({}, { 'PATCH /api/settings': apiError(409, 'bike_busy') });
    renderWithClient(<BikeForm />);
    await userEvent.type(await screen.findByLabelText('Bike address'), 'AA');
    await userEvent.click(screen.getByRole('button', { name: /save/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Release the bike on the Ride screen first');
  });
});
