import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithClient, settingsFixture, stubApi } from '@/test-utils';
import { HeartRateForm } from './Bike';

const devices = [
  { address: 'AA:BB:CC:DD:EE:FF', name: 'MRK-S29R2', protocol: 'fitplus', rssi: -60 },
  { address: 'AA:BB:CC:DD:EE:01', name: 'HeartCast', protocol: 'hr', rssi: -70 },
];

function setup(hr: Partial<ReturnType<typeof settingsFixture>['heart_rate']> = {}, extra: Parameters<typeof stubApi>[0] = {}) {
  const s = settingsFixture({ heart_rate: hr });
  return stubApi({
    'GET /api/settings': [200, s],
    'PATCH /api/settings': [200, s],
    'POST /api/bike/scan': [200, { devices }],
    'GET /api/ha/entities': [200, { entities: [], truncated: false }],
    ...extra,
  });
}

describe('HeartRateForm', () => {
  it('scans, lists only heart rate monitors and fills address and name', async () => {
    const api = setup();
    renderWithClient(<HeartRateForm />);
    await userEvent.click(await screen.findByRole('radio', { name: 'Bluetooth' }));
    expect(screen.getByText(/run HeartCast on an iPhone/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Scan' }));
    expect(await screen.findByRole('row', { name: /HeartCast/ })).toBeInTheDocument();
    expect(screen.queryByText('MRK-S29R2')).toBeNull();
    await userEvent.click(screen.getByRole('row', { name: /HeartCast/ }));
    expect(screen.getByLabelText('Monitor address')).toHaveValue('AA:BB:CC:DD:EE:01');
    expect(screen.getByLabelText('Monitor name')).toHaveValue('HeartCast');
    await userEvent.click(screen.getByRole('button', { name: /save/i }));
    await waitFor(() => expect(api.bodies('PATCH /api/settings')).toHaveLength(1));
    expect(api.bodies('PATCH /api/settings')[0]).toEqual({
      heart_rate: { source: 'ble', address: 'AA:BB:CC:DD:EE:01', name: 'HeartCast' },
    });
  });

  it('Home Assistant shows the entity picker and saves the entity', async () => {
    const api = setup({}, {
      'GET /api/ha/entities': [
        200,
        { entities: [{ entity_id: 'sensor.hr', name: 'Heart rate', state: '70', unit: 'bpm', device_class: '' }], truncated: false },
      ],
    });
    renderWithClient(<HeartRateForm />);
    await userEvent.click(await screen.findByRole('radio', { name: 'Home Assistant' }));
    expect(screen.queryByLabelText('Monitor address')).toBeNull();
    await userEvent.type(screen.getByLabelText('Heart rate entity'), 'heart');
    await userEvent.click(await screen.findByRole('option', { name: 'Heart rate (sensor.hr)' }));
    await userEvent.click(screen.getByRole('button', { name: /save/i }));
    await waitFor(() => expect(api.bodies('PATCH /api/settings')).toHaveLength(1));
    expect(api.bodies('PATCH /api/settings')[0]).toEqual({ heart_rate: { source: 'ha', ha_entity: 'sensor.hr' } });
  });

  it('Off hides every source field', async () => {
    setup({ source: 'ble', address: 'AA:BB:CC:DD:EE:01', name: 'HeartCast' });
    renderWithClient(<HeartRateForm />);
    await userEvent.click(await screen.findByRole('radio', { name: 'Off' }));
    expect(screen.queryByLabelText('Monitor address')).toBeNull();
  });
});
