import { act, render, screen } from '@testing-library/react';
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

  it('a button stays disabled until its ack', async () => {
    let ack!: () => void;
    const send = vi.fn(() => new Promise<void>((r) => (ack = r)));
    render(<RideControls state="ready" send={send} />);
    await userEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    await act(async () => ack());
    expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
  });

  it('Enter and Space run the primary action once', async () => {
    const { send } = setup('riding');
    await userEvent.keyboard('{Enter}');
    expect(send).toHaveBeenCalledWith('pause');
    await userEvent.keyboard(' ');
    expect(send).toHaveBeenCalledTimes(2);
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
