import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RidePage } from './Ride';
import { apiError, renderWithClient, stubApi } from '@/test-utils';
import { bikeFixture, FakeWebSocket, tickFixture } from '@/test-ws';
import type { components } from '@/lib/openapi';
import type { BikeStatus, Tick } from '@/lib/ws-types';

const rider = (id: number, name: string) =>
  ({ id, name, min_level: 1, max_level: 16 }) as components['schemas']['RiderOut'];

const riders = [rider(1, 'Ann'), rider(2, 'Bo')];

beforeEach(() => {
  FakeWebSocket.reset();
  vi.stubGlobal('WebSocket', FakeWebSocket);
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  localStorage.clear();
});

/** Render, open the socket and push the first state message. */
async function setup(bike: Partial<BikeStatus> = {}, tick: Partial<Tick> = {}, routes: Parameters<typeof stubApi>[0] = {}) {
  const stub = stubApi({ 'GET /api/riders': [200, riders], ...routes });
  renderWithClient(<RidePage />);
  const ws = FakeWebSocket.last;
  act(() => {
    ws.open();
    ws.receive({ type: 'state', bike: bikeFixture(bike), tick: tickFixture(tick) });
  });
  await screen.findByRole('option', { name: 'Ann' });
  return { ws, stub };
}

const push = (ws: FakeWebSocket, msg: unknown) => act(() => ws.receive(msg));

describe('RidePage', () => {
  it('idle: Connect POSTs the chosen rider', async () => {
    const { stub } = await setup({ state: 'idle', connected: false, rider_id: undefined }, { state: 'idle' }, {
      'POST /api/bike/connect': [202, bikeFixture({ state: 'connecting' })],
    });
    expect(screen.queryByRole('button', { name: 'Release' })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Connect' }));
    await waitFor(() => expect(stub.bodies('POST /api/bike/connect')).toEqual([{ rider_id: 1 }]));
  });

  it('ready shows Start; riding shows Pause and Stop; Stop needs two taps', async () => {
    const { ws } = await setup({ state: 'ready' }, { state: 'ready' });
    expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Release' })).toBeInTheDocument();
    push(ws, { type: 'tick', tick: tickFixture({ state: 'riding' }) });
    expect(screen.getByRole('button', { name: 'Pause' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Stop' }));
    expect(ws.commands.filter((c) => c.command === 'stop')).toHaveLength(0);
    await userEvent.click(screen.getByRole('button', { name: /Tap again to stop/ }));
    expect(ws.commands.at(-1)).toMatchObject({ command: 'stop' });
  });

  it('ready: focus starts on Start; Enter sends start, ArrowUp tunes the level from there', async () => {
    const { ws } = await setup({ state: 'ready' }, { state: 'ready' });
    expect(screen.getByRole('button', { name: 'Start' })).toHaveFocus();
    await userEvent.keyboard('{ArrowUp}');
    expect(ws.commands.at(-1)).toMatchObject({ command: 'level_delta', value: 1 });
    await userEvent.keyboard('{Enter}');
    expect(ws.commands.at(-1)).toMatchObject({ command: 'start' });
  });

  it('riding with focus on the page: ArrowUp/ArrowDown change the level by 1', async () => {
    const { ws } = await setup({ state: 'riding' }, { state: 'riding' });
    (document.activeElement as HTMLElement).blur();
    await userEvent.keyboard('{ArrowUp}');
    expect(ws.commands.at(-1)).toMatchObject({ command: 'level_delta', value: 1 });
    await userEvent.keyboard('{ArrowDown}');
    expect(ws.commands.at(-1)).toMatchObject({ command: 'level_delta', value: -1 });
  });

  it('riding: ArrowRight moves focus from Pause to Stop and sends nothing', async () => {
    const { ws } = await setup({ state: 'riding' }, { state: 'riding' });
    const pause = screen.getByRole('button', { name: 'Pause' });
    expect(pause).toHaveFocus();
    const before = ws.commands.length;
    await userEvent.keyboard('{ArrowRight}');
    expect(screen.getByRole('button', { name: 'Stop' })).toHaveFocus();
    expect(ws.commands).toHaveLength(before);
  });

  it('Escape and Backspace go back; Escape in a text field does not', async () => {
    const back = vi.spyOn(history, 'back').mockImplementation(() => {});
    await setup({ state: 'ready' }, { state: 'ready' });
    await userEvent.keyboard('{Escape}');
    expect(back).toHaveBeenCalledTimes(1);
    await userEvent.keyboard('{Backspace}');
    expect(back).toHaveBeenCalledTimes(2);
    await userEvent.click(screen.getByLabelText('Ride'));
    back.mockClear();
    await userEvent.keyboard('{Escape}{Backspace}');
    expect(back).not.toHaveBeenCalled();
    back.mockRestore();
  });

  it('disconnected: every ride button is disabled and Reconnecting is shown', async () => {
    const { ws } = await setup({ state: 'riding' }, { state: 'riding' });
    expect(screen.queryByText(/Reconnecting/)).not.toBeInTheDocument();
    act(() => ws.drop(1006));
    expect(screen.getByText(/Reconnecting/)).toBeInTheDocument();
    for (const name of ['Pause', 'Stop', 'Increase level', 'Decrease level']) {
      expect(screen.getByRole('button', { name })).toBeDisabled();
    }
    // releasing the bike is an HTTP call and stays available
    expect(screen.getByRole('button', { name: 'Release' })).toBeEnabled();
  });

  it('renders last_error and hint from the bike status', async () => {
    await setup(
      { state: 'idle', connected: false, last_error: 'connect failed: timeout', hint: 'Turn the bike on and wake it.' },
      { state: 'idle' },
    );
    expect(screen.getByText(/connect failed: timeout/)).toBeInTheDocument();
    expect(screen.getByText('Turn the bike on and wake it.')).toBeInTheDocument();
  });

  it('floor_unconfirmed on the bike status asks to lower the resistance, wording aside', async () => {
    await setup({ state: 'idle', connected: false, last_error: 'bike not confirmed', floor_unconfirmed: true });
    const alerts = screen.getAllByRole('alert').filter((a) => /lower the resistance/i.test(a.textContent ?? ''));
    expect(alerts).toHaveLength(1);
    expect(alerts[0]).toHaveTextContent('Bike may still be at level 5 — lower the resistance at the console.');
    expect(alerts[0]).toHaveTextContent('bike not confirmed');
  });

  it('a last_error that merely mentions confirm is not a floor warning', async () => {
    await setup({ state: 'idle', connected: false, last_error: 'could not confirm the minimum level of the adapter' });
    expect(screen.queryByText(/lower the resistance/i)).not.toBeInTheDocument();
  });

  it('release 424 floor_unconfirmed warns about the level', async () => {
    await setup({ state: 'riding' }, { state: 'riding', level: 9, reported_level: 9 }, {
      'POST /api/bike/release': apiError(424, 'floor_unconfirmed', 'could not confirm'),
    });
    await userEvent.click(screen.getByRole('button', { name: 'Release' }));
    expect(await screen.findByText(/Bike may still be at level 9/)).toBeInTheDocument();
    expect(screen.getAllByRole('alert').filter((a) => /lower the resistance/i.test(a.textContent ?? ''))).toHaveLength(1);
  });

  it('server floor status after a 424 still shows exactly one banner', async () => {
    const { ws } = await setup({ state: 'riding' }, { state: 'riding', level: 9, reported_level: 9 }, {
      'POST /api/bike/release': apiError(424, 'floor_unconfirmed', 'could not confirm'),
    });
    await userEvent.click(screen.getByRole('button', { name: 'Release' }));
    await screen.findByText(/Bike may still be at level 9/);
    push(ws, { type: 'state', bike: bikeFixture({ state: 'idle', last_error: 'bike not confirmed', floor_unconfirmed: true }), tick: tickFixture({ state: 'idle', level: 9, reported_level: 9 }) });
    expect(screen.getAllByRole('alert').filter((a) => /lower the resistance/i.test(a.textContent ?? ''))).toHaveLength(1);
    expect(screen.getAllByText(/level 9/)).toHaveLength(1);
  });

  it('keeps a tick alert while present and at least 10 s', async () => {
    const { ws } = await setup({ state: 'riding' }, { state: 'riding' });
    vi.useFakeTimers();
    const advance = (ms: number) => act(() => void vi.advanceTimersByTime(ms));
    push(ws, { type: 'tick', tick: tickFixture({ state: 'riding', alert: 'No reading from the bike' }) });
    expect(screen.getByText('No reading from the bike')).toBeInTheDocument();
    advance(30_000); // still reported: still shown
    expect(screen.getByText('No reading from the bike')).toBeInTheDocument();
    push(ws, { type: 'tick', tick: tickFixture({ state: 'riding' }) }); // cleared after 30 s: gone at once
    advance(0);
    expect(screen.queryByText('No reading from the bike')).not.toBeInTheDocument();

    push(ws, { type: 'tick', tick: tickFixture({ state: 'riding', alert: 'Stale' }) });
    advance(4_000);
    push(ws, { type: 'tick', tick: tickFixture({ state: 'riding' }) });
    advance(5_999);
    expect(screen.getByText('Stale')).toBeInTheDocument();
    advance(1);
    expect(screen.queryByText('Stale')).not.toBeInTheDocument();
  });

  it('a rejected command is shown', async () => {
    const { ws } = await setup({ state: 'ready' }, { state: 'ready' });
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    push(ws, { type: 'error', id: 'c1', error: { code: 'bad_state', message: 'cannot start now' } });
    expect(await screen.findByText(/cannot start now/)).toBeInTheDocument();
  });
});
