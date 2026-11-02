import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { LevelControls } from './LevelControls';
import { TickStore } from '@/lib/tick-store';
import { tickFixture } from '@/test-ws';

beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
afterEach(() => vi.useRealTimers());

describe('LevelControls', () => {
  it('5 taps send 5 level_delta and display follows ticks', async () => {
    const ticks = new TickStore();
    ticks.set(tickFixture({ level: 5 }));
    const send = vi.fn().mockResolvedValue(undefined);
    render(<LevelControls socket={{ send, ticks }} />);
    expect(screen.getByText('L5')).toBeInTheDocument();
    const up = screen.getByRole('button', { name: 'Increase level' });
    for (let i = 0; i < 5; i++) await userEvent.click(up);
    expect(send).toHaveBeenCalledTimes(5);
    expect(send).toHaveBeenNthCalledWith(1, 'level_delta', 1);
    expect(screen.getByText('L5')).toBeInTheDocument(); // only a tick moves it
    act(() => {
      ticks.set(tickFixture({ level: 7 }));
      vi.runAllTimers();
    });
    expect(screen.getByText('L7')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Decrease level' }));
    expect(send).toHaveBeenLastCalledWith('level_delta', -1);
  });

  it('arrow keys change the level; Left/Down lower it', async () => {
    const ticks = new TickStore();
    const send = vi.fn().mockResolvedValue(undefined);
    render(<LevelControls socket={{ send, ticks }} />);
    await userEvent.keyboard('{ArrowUp}{ArrowRight}{ArrowDown}{ArrowLeft}');
    expect(send.mock.calls).toEqual([
      ['level_delta', 1],
      ['level_delta', 1],
      ['level_delta', -1],
      ['level_delta', -1],
    ]);
  });

  it('is inert when disabled', async () => {
    const send = vi.fn().mockResolvedValue(undefined);
    render(<LevelControls socket={{ send, ticks: new TickStore() }} disabled />);
    expect(screen.getByRole('button', { name: 'Increase level' })).toBeDisabled();
    await userEvent.keyboard('{ArrowUp}');
    expect(send).not.toHaveBeenCalled();
  });

  it('reports a rejected command', async () => {
    const onError = vi.fn();
    const send = vi.fn().mockRejectedValue(new Error('bad_state'));
    render(<LevelControls socket={{ send, ticks: new TickStore() }} onError={onError} />);
    await userEvent.click(screen.getByRole('button', { name: 'Increase level' }));
    expect(onError).toHaveBeenCalled();
  });

  it('route mode: Easier/Harder send level_delta and the bias is shown', async () => {
    const ticks = new TickStore();
    const send = vi.fn().mockResolvedValue(undefined);
    render(<LevelControls socket={{ send, ticks }} route />);
    await userEvent.click(screen.getByRole('button', { name: 'Harder' }));
    await userEvent.click(screen.getByRole('button', { name: 'Easier' }));
    expect(send.mock.calls).toEqual([
      ['level_delta', 1],
      ['level_delta', -1],
    ]);
    act(() => {
      ticks.set(tickFixture({ bias: -2 }));
      vi.runAllTimers();
    });
    expect(screen.getByText('Bias -2')).toBeInTheDocument();
  });
});
