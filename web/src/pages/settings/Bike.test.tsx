import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { components } from '@/lib/openapi';
import { renderWithClient, settingsFixture, stubApi } from '@/test-utils';
import { BikeForm, PowerModelCard } from './Bike';

const model = (over: Partial<components['schemas']['PowerModelInfo']> = {}): components['schemas']['PowerModelInfo'] => ({
  a: 0.375,
  b: 0.25,
  bike_key: 'sim',
  samples: 42,
  seeded: false,
  watts_at_80: Array.from({ length: 16 }, (_, i) => 100 + i * 10),
  ...over,
});

describe('PowerModelCard', () => {
  it('shows samples and watts at L1, L8 and L16', async () => {
    stubApi({ 'GET /api/bike/power-model': [200, model()] });
    renderWithClient(<PowerModelCard />);
    expect(await screen.findByText('Learned power model')).toBeInTheDocument();
    expect(await screen.findByText('42')).toBeInTheDocument();
    expect(screen.getByTestId('pm-l1')).toHaveTextContent('100 W');
    expect(screen.getByTestId('pm-l8')).toHaveTextContent('170 W');
    expect(screen.getByTestId('pm-l16')).toHaveTextContent('250 W');
  });

  it('Reset needs a second tap, then DELETEs and reloads', async () => {
    let current = model();
    const stub = stubApi({
      'GET /api/bike/power-model': () => [200, current],
      'DELETE /api/bike/power-model': () => {
        current = model({ samples: 0, seeded: true });
        return [204];
      },
    });
    renderWithClient(<PowerModelCard />);
    await screen.findByText('42');
    await userEvent.click(screen.getByRole('button', { name: 'Reset' }));
    expect(screen.getByRole('status')).toHaveTextContent(/Tap Reset again/);
    expect(stub.calls.some((c) => c.key === 'DELETE /api/bike/power-model')).toBe(false);
    await userEvent.click(screen.getByRole('button', { name: /Tap again to reset/ }));
    await waitFor(() => expect(stub.calls.some((c) => c.key === 'DELETE /api/bike/power-model')).toBe(true));
    expect(await screen.findByText('0')).toBeInTheDocument();
  });

  it('says so when the model is still the built-in one', async () => {
    stubApi({ 'GET /api/bike/power-model': [200, model({ samples: 0, seeded: true })] });
    renderWithClient(<PowerModelCard />);
    expect(await screen.findByText(/built-in starting model/)).toBeInTheDocument();
  });
});

describe('PowerModelCard and bike settings', () => {
  it('refetches the power model once the bike settings are saved', async () => {
    const st = settingsFixture({ bike: { driver: 'fitplus' } });
    let current = model();
    const stub = stubApi({
      'GET /api/settings': [200, st],
      'PATCH /api/settings': [200, st],
      'GET /api/bike/power-model': () => [200, current],
    });
    renderWithClient(
      <>
        <BikeForm />
        <PowerModelCard />
      </>,
    );
    await screen.findByText('42');
    const gets = () => stub.calls.filter((c) => c.key === 'GET /api/bike/power-model').length;
    expect(gets()).toBe(1);
    current = model({ samples: 7, bike_key: 'other' });
    const idle = await screen.findByLabelText(/idle auto-release/i);
    await userEvent.clear(idle);
    await userEvent.type(idle, '30');
    await userEvent.click(screen.getByRole('button', { name: /save/i }));
    expect(await screen.findByText('7')).toBeInTheDocument();
    expect(gets()).toBe(2);
  });
});
