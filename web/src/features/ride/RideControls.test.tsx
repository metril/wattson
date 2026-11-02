import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RideControls } from './RideControls';

const setup = (state: Parameters<typeof RideControls>[0]['state'], over = {}) => {
  const send = vi.fn().mockResolvedValue(undefined);
  const utils = render(<RideControls state={state} send={send} {...over} />);
  return { send, ...utils };
};

describe('RideControls', () => {
  it('ready shows only Start', async () => {
    const { send } = setup('ready');
    expect(screen.queryByRole('button', { name: /stop/i })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(send).toHaveBeenCalledWith('start');
  });

  it('riding shows Pause and Stop; paused shows Resume and Stop', () => {
    const a = setup('riding');
    expect(screen.getByRole('button', { name: 'Pause' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument();
    a.unmount();
    setup('paused');
    expect(screen.getByRole('button', { name: 'Resume' })).toBeInTheDocument();
  });

  it('Stop needs a second tap', async () => {
    const { send } = setup('riding');
    await userEvent.click(screen.getByRole('button', { name: 'Stop' }));
    expect(send).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole('button', { name: /Tap again to stop/ }));
    expect(send).toHaveBeenCalledWith('stop');
  });

  it('stays disabled until acked, then until the state changes', async () => {
    let ack!: () => void;
    const send = vi.fn(() => new Promise<void>((r) => (ack = r)));
    const { rerender } = render(<RideControls state="ready" send={send} />);
    await userEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    await act(async () => ack());
    // acked but the state has not flipped yet: a second Start would be stale
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    rerender(<RideControls state="riding" send={send} />);
    expect(screen.getByRole('button', { name: 'Pause' })).toBeEnabled();
  });

  it('re-enables 2 s after the ack when the state never changes', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      render(<RideControls state="ready" send={vi.fn().mockResolvedValue(undefined)} />);
      await userEvent.click(screen.getByRole('button', { name: 'Start' }));
      expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
      await act(async () => void vi.advanceTimersByTime(2000));
      expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('re-enables at once when the command is rejected', async () => {
    const onError = vi.fn();
    render(<RideControls state="ready" send={vi.fn().mockRejectedValue(new Error('x'))} onError={onError} />);
    await userEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(onError).toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
  });

  it('ignores auto-repeat keydown events', () => {
    const { send } = setup('ready');
    fireEvent.keyDown(window, { key: 'Enter', repeat: true });
    expect(send).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: 'Enter' });
    expect(send).toHaveBeenCalledTimes(1);
  });

  it('a held Enter does not repeat the command', async () => {
    const { send } = setup('ready');
    await userEvent.keyboard('{Enter>3/}');
    expect(send).toHaveBeenCalledTimes(1);
  });

  it('Enter and Space run the primary action', async () => {
    const a = setup('riding');
    await userEvent.keyboard('{Enter}');
    expect(a.send).toHaveBeenCalledWith('pause');
    a.unmount();
    const b = setup('paused');
    await userEvent.keyboard(' ');
    expect(b.send).toHaveBeenCalledWith('resume');
  });

  it('Enter on a focused button does not also fire the primary action', async () => {
    const { send } = setup('riding');
    const stop = screen.getByRole('button', { name: 'Stop' });
    stop.focus();
    await userEvent.keyboard('{Enter}');
    expect(send).not.toHaveBeenCalled(); // first Stop tap only arms the confirmation
    expect(stop).toHaveTextContent(/again/);
  });

  it('disables every button when disabled', () => {
    setup('riding', { disabled: true });
    for (const b of screen.getAllByRole('button')) expect(b).toBeDisabled();
  });
});
