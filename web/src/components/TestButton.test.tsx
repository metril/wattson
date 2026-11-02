import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ApiError } from '@/lib/api';
import { TestButton } from './TestButton';

describe('TestButton', () => {
  it('shows failure as an alert', async () => {
    render(<TestButton run={async () => ({ ok: false, message: 'token rejected (401)', latency_ms: 12 })} />);
    await userEvent.click(screen.getByRole('button', { name: /test/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent('token rejected (401)');
    expect(screen.getByRole('alert')).toHaveTextContent('12 ms');
  });

  it('shows success as status', async () => {
    render(<TestButton run={async () => ({ ok: true, message: 'connected', latency_ms: 40 })} />);
    await userEvent.click(screen.getByRole('button', { name: /test/i }));
    expect(await screen.findByRole('status')).toHaveTextContent('connected');
  });

  it('is busy while running', async () => {
    let done!: () => void;
    render(
      <TestButton
        run={() => new Promise((r) => { done = () => r({ ok: true, message: 'ok', latency_ms: 1 }); })}
      />,
    );
    await userEvent.click(screen.getByRole('button', { name: /test/i }));
    expect(screen.getByRole('button', { name: /test/i })).toBeDisabled();
    done();
    expect(await screen.findByRole('status')).toBeInTheDocument();
  });

  it('turns a thrown ApiError into a failure, with the bike_busy hint', async () => {
    render(<TestButton label="Test BLE" run={async () => { throw new ApiError(409, 'bike_busy', 'busy'); }} />);
    await userEvent.click(screen.getByRole('button', { name: 'Test BLE' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Release the bike on the Ride screen first');
  });
});
