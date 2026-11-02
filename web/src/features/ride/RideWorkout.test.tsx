import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RidePage } from '@/pages/Ride';
import { renderWithClient, stubApi } from '@/test-utils';
import { bikeFixture, FakeWebSocket, tickFixture } from '@/test-ws';
import type { components } from '@/lib/openapi';
import type { Tick } from '@/lib/ws-types';

type Workout = components['schemas']['WorkoutOut'];

const rider = { id: 1, name: 'Ann', min_level: 1, max_level: 16, offset_level: 5, ftp_w: 200 } as components['schemas']['RiderOut'];
const workout = (over: Partial<Workout> = {}): Workout => ({
  id: 5,
  name: 'Tempo',
  type: 'interval',
  owner_id: 1,
  duration_s: 150,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  blocks: [
    { duration_s: 75, level: 4, label: 'Warm up', cadence: '80-90 rpm' },
    { duration_s: 75, level_offset: 2 },
  ],
  ...over,
});

beforeEach(() => {
  FakeWebSocket.reset();
  vi.stubGlobal('WebSocket', FakeWebSocket);
  // rAF fires synchronously-ish under fake timers; the tests flush it with runAllTimers.
  vi.useFakeTimers({ shouldAdvanceTime: true });
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  localStorage.clear();
});

async function setup(route: string, tick: Partial<Tick> = {}, routes: Parameters<typeof stubApi>[0] = {}) {
  const stub = stubApi({
    'GET /api/riders': [200, [rider]],
    'GET /api/routes': [200, []],
    'GET /api/workouts': [200, [workout()]],
    'GET /api/workouts/5': [200, workout()],
    ...routes,
  });
  renderWithClient(<RidePage />, route);
  const ws = FakeWebSocket.last;
  act(() => {
    ws.open();
    ws.receive({ type: 'state', bike: bikeFixture({ state: 'ready' }), tick: tickFixture({ state: 'ready', ...tick }) });
  });
  await screen.findByRole('option', { name: 'Ann' });
  return { ws, stub };
}

const push = (ws: FakeWebSocket, tick: Partial<Tick>) =>
  act(() => {
    ws.receive({ type: 'tick', tick: tickFixture(tick) });
    vi.runOnlyPendingTimers();
  });

describe('Ride workout and ERG', () => {
  it('?workout=5 preselects the workout and Start sends workout_id', async () => {
    const { ws } = await setup('/?workout=5');
    await screen.findByRole('option', { name: 'Tempo' });
    expect(screen.getByLabelText('Ride')).toHaveValue('workout:5');
    await userEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(ws.commands.at(-1)).toMatchObject({ command: 'start', workout_id: 5 });
    expect(ws.commands.at(-1)).not.toHaveProperty('route_id');
  });

  it('a ?workout= that is not in the list falls back to a free ride', async () => {
    await setup('/?workout=9');
    await waitFor(() => expect(screen.getByLabelText('Ride')).toHaveValue('free'));
  });

  it('shows position, target, next block and countdown from ticks; fetches the workout once', async () => {
    const { ws, stub } = await setup('/?workout=5');
    push(ws, { state: 'riding', mode: 'interval', workout_id: 5, block_index: 0, block_count: 2, block_remaining_s: 75, level: 4 });
    expect(await screen.findByTestId('block-pos')).toHaveTextContent('1/2');
    expect(screen.getByTestId('block-target')).toHaveTextContent('L4');
    expect(screen.getByTestId('block-label')).toHaveTextContent('Warm up');
    expect(screen.getByTestId('block-cadence')).toHaveTextContent('80-90 rpm');
    expect(screen.getByTestId('next-block')).toHaveTextContent('+2 → L7');
    push(ws, { state: 'riding', mode: 'interval', workout_id: 5, block_index: 1, block_count: 2, block_remaining_s: 75, level: 7 });
    expect(screen.getByTestId('block-pos')).toHaveTextContent('2/2');
    expect(screen.getByTestId('block-countdown')).toHaveTextContent('1:15');
    expect(screen.getByTestId('block-target')).toHaveTextContent('+2 → L7');
    expect(screen.getByTestId('next-block')).toHaveTextContent('Last block');
    expect(stub.calls.filter((c) => c.key === 'GET /api/workouts/5')).toHaveLength(1);
    // the cursor sits at 75 + 0 s of 150 s
    expect(screen.getByTestId('workout-cursor').getAttribute('x1')).toBe('500');
  });

  it('interval mode keeps Easier/Harder and the bias', async () => {
    const { ws } = await setup('/?workout=5');
    push(ws, { state: 'riding', mode: 'interval', workout_id: 5, block_index: 0, block_count: 2, block_remaining_s: 10, bias: 2 });
    expect(await screen.findByRole('button', { name: 'Harder' })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText('Bias +2')).toBeInTheDocument());
  });

  it('ERG workout targets read as watts and % FTP', async () => {
    const erg = workout({
      type: 'erg',
      blocks: [
        { duration_s: 60, power_w: 250 },
        { duration_s: 60, ftp_pct: 75 },
      ],
    });
    const { ws } = await setup('/?workout=5', {}, { 'GET /api/workouts/5': [200, erg], 'GET /api/workouts': [200, [erg]] });
    push(ws, { state: 'riding', mode: 'erg', workout_id: 5, block_index: 0, block_count: 2, block_remaining_s: 60, target_power_w: 250 });
    expect(await screen.findByTestId('block-target')).toHaveTextContent('250 W');
    expect(screen.getByTestId('next-block')).toHaveTextContent('75 % FTP · 150 W');
  });

  it('free ERG: choosing 200 W starts with target_power_w', async () => {
    const { ws } = await setup('/');
    await userEvent.selectOptions(screen.getByLabelText('Ride'), 'erg');
    const watts = screen.getByLabelText('Target power (W)');
    expect(watts).toHaveValue(150);
    await userEvent.clear(watts);
    await userEvent.type(watts, '200');
    await userEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(ws.commands.at(-1)).toMatchObject({ command: 'start', target_power_w: 200 });
  });

  it('erg mode: +10 W nudges, the input sets the target, Target W is shown', async () => {
    const { ws } = await setup('/');
    push(ws, { state: 'riding', mode: 'erg', target_power_w: 250, power_w: 240 });
    expect(await screen.findByText('Target')).toBeInTheDocument();
    expect(screen.getByText('250')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Harder' })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '+10 W' }));
    expect(ws.commands.at(-1)).toMatchObject({ command: 'level_delta', value: 1 });
    await userEvent.click(screen.getByRole('button', { name: '−10 W' }));
    expect(ws.commands.at(-1)).toMatchObject({ command: 'level_delta', value: -1 });
    const watts = screen.getByLabelText('Target power (W)');
    await userEvent.clear(watts);
    await userEvent.type(watts, '260');
    await userEvent.click(screen.getByRole('button', { name: 'Set' }));
    expect(ws.commands.at(-1)).toMatchObject({ command: 'set_target_power', value: 260 });
  });

  it('an ERG workout has no numeric target input', async () => {
    const { ws } = await setup('/');
    push(ws, { state: 'riding', mode: 'erg', workout_id: 5, block_index: 0, block_count: 2, block_remaining_s: 5, target_power_w: 250 });
    expect(await screen.findByRole('button', { name: '+10 W' })).toBeInTheDocument();
    expect(screen.queryByLabelText('Target power (W)')).not.toBeInTheDocument();
  });

  it('a finished workout shows Workout complete; a stopped one does not', async () => {
    const { ws } = await setup('/?workout=5');
    push(ws, { state: 'riding', mode: 'interval', workout_id: 5, block_index: 1, block_count: 2, block_remaining_s: 30 });
    push(ws, { state: 'finished', mode: 'interval', workout_id: 5, block_index: 1, block_count: 2, block_remaining_s: 30 });
    expect(screen.queryByText('Workout complete')).not.toBeInTheDocument();
    push(ws, { state: 'riding', mode: 'interval', workout_id: 5, block_index: 1, block_count: 2, block_remaining_s: 5 });
    push(ws, { state: 'finished', mode: 'interval', workout_id: 5, block_index: 1, block_count: 2, block_remaining_s: 0 });
    await waitFor(() => expect(screen.getByText('Workout complete')).toBeInTheDocument());
  });
});
