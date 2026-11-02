import { useState } from 'react';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { apiError, renderWithClient, stubApi } from '@/test-utils';
import { EntityPicker } from './EntityPicker';

const entities = [
  { entity_id: 'number.bike_resistance', name: 'Bike resistance', state: '5', unit: '', device_class: '' },
  { entity_id: 'number.fan_speed', name: 'Fan speed', state: '1', unit: '', device_class: '' },
];

function Harness({ onChange }: { onChange: (id: string) => void }) {
  const [v, setV] = useState('');
  return (
    <EntityPicker
      domain="number"
      label="Resistance entity"
      value={v}
      onChange={(id) => {
        setV(id);
        onChange(id);
      }}
    />
  );
}

describe('EntityPicker', () => {
  it('lists matches by name and picks one', async () => {
    const api = stubApi({ 'GET /api/ha/entities': [200, { entities, truncated: false }] });
    const onChange = vi.fn();
    renderWithClient(<Harness onChange={onChange} />);
    await userEvent.type(await screen.findByLabelText('Resistance entity'), 'resist');
    expect(screen.queryByText('Fan speed (number.fan_speed)')).toBeNull();
    await userEvent.click(await screen.findByRole('option', { name: 'Bike resistance (number.bike_resistance)' }));
    expect(onChange).toHaveBeenLastCalledWith('number.bike_resistance');
    expect(screen.getByLabelText('Resistance entity')).toHaveValue('number.bike_resistance');
    expect(api.calls[0].key).toBe('GET /api/ha/entities');
  });

  it('puts entities of the wanted device class first', async () => {
    const list = [
      { entity_id: 'sensor.a_temp', name: 'A temp', state: '1', unit: '', device_class: 'temperature' },
      { entity_id: 'sensor.z_power', name: 'Z power', state: '1', unit: 'W', device_class: 'power' },
    ];
    stubApi({ 'GET /api/ha/entities': [200, { entities: list, truncated: false }] });
    renderWithClient(<EntityPicker domain="sensor" deviceClass="power" label="Power" value="" onChange={() => {}} />);
    await userEvent.click(await screen.findByLabelText('Power'));
    const opts = await screen.findAllByRole('option');
    expect(opts.map((o) => o.textContent)).toEqual(['Z power (sensor.z_power)', 'A temp (sensor.a_temp)']);
  });

  it('keeps free text and shows the server message on 502', async () => {
    stubApi({ 'GET /api/ha/entities': apiError(502, 'ha_unavailable', 'Home Assistant rejected the token') });
    const onChange = vi.fn();
    renderWithClient(<Harness onChange={onChange} />);
    expect(await screen.findByText('Home Assistant rejected the token')).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText('Resistance entity'), 'n');
    expect(onChange).toHaveBeenLastCalledWith('n');
  });

  it('notes a truncated list', async () => {
    stubApi({ 'GET /api/ha/entities': [200, { entities, truncated: true }] });
    renderWithClient(<Harness onChange={() => {}} />);
    expect(await screen.findByText(/list was cut/i)).toBeInTheDocument();
  });
});
