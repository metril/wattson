import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Route, Routes, useLocation } from 'react-router';
import { WorkoutEditPage } from './WorkoutEdit';
import { WorkoutsPage } from './Workouts';
import type { components } from '@/lib/openapi';
import { apiError, renderWithClient, stubApi, userFixture } from '@/test-utils';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

type Rider = components['schemas']['RiderOut'];
type Workout = components['schemas']['WorkoutOut'];

const rider = (over: Partial<Rider> = {}): Rider => ({
  id: 1,
  user_id: 1,
  name: 'Ann',
  weight_kg: 70,
  bike_kg: 8,
  ftp_w: 200,
  cda: 0.32,
  crr: 0.004,
  power_multiplier: 1,
  min_level: 1,
  max_level: 16,
  offset_level: 5,
  gain: 1,
  downhill_scale: 0.5,
  lead_s: 4,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  ...over,
});

const workout = (over: Partial<Workout> = {}): Workout => ({
  id: 7,
  name: 'Sweet spot',
  type: 'interval',
  owner_id: 1,
  duration_s: 180,
  blocks: [{ duration_s: 60, level: 4 }, { duration_s: 120, level: 8 }],
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  ...over,
});

const me = (over = {}) =>
  [200, { user: userFixture({ id: 1, is_admin: false, ...over }), scopes: [], via: 'session' }] as [number, unknown];

const Where = () => {
  const l = useLocation();
  return <p data-testid="where">{l.pathname + l.search}</p>;
};
const ui = (
  <Routes>
    <Route path="/workouts" element={<WorkoutsPage />} />
    <Route path="/workouts/new" element={<WorkoutEditPage />} />
    <Route path="/workouts/:id" element={<WorkoutEditPage />} />
    <Route path="*" element={<Where />} />
  </Routes>
);

function base(over: Parameters<typeof stubApi>[0] = {}) {
  return stubApi({
    'GET /api/auth/me': me(),
    'GET /api/riders': [200, [rider()]],
    'GET /api/workouts': [200, [workout()]],
    'GET /api/workouts/7': [200, workout()],
    ...over,
  });
}

describe('WorkoutsPage', () => {
  it('lists workouts with type and duration, and offers New', async () => {
    base();
    renderWithClient(ui, '/workouts');
    const link = await screen.findByRole('link', { name: /Sweet spot/ });
    expect(link).toHaveAttribute('href', '/workouts/7');
    expect(within(link).getByText('Interval')).toBeInTheDocument();
    expect(within(link).getByText('3:00')).toBeInTheDocument();
    expect(link.querySelectorAll('rect')).toHaveLength(2);
    expect(screen.getByRole('link', { name: 'New workout' })).toHaveAttribute('href', '/workouts/new');
  });

  it('shows an empty state and server errors', async () => {
    base({ 'GET /api/workouts': [200, []] });
    renderWithClient(ui, '/workouts');
    expect(await screen.findByText('No workouts yet')).toBeInTheDocument();
    vi.unstubAllGlobals();
    base({ 'GET /api/workouts': apiError(500, 'internal', 'database is locked') });
    renderWithClient(ui, '/workouts');
    expect(await screen.findByRole('alert')).toHaveTextContent('database is locked');
  });
});

describe('WorkoutEditPage', () => {
  it('creates an interval workout with the typed blocks', async () => {
    const made = workout({ id: 9, name: 'Mine', blocks: [{ duration_s: 120, level: 6 }], duration_s: 120 });
    const stub = base({ 'POST /api/workouts': [201, made], 'GET /api/workouts/9': [200, made] });
    renderWithClient(ui, '/workouts/new');
    await userEvent.type(await screen.findByLabelText('Name'), 'Mine');
    const dur = screen.getByLabelText('Block 1 duration');
    await userEvent.clear(dur);
    await userEvent.type(dur, '2:00');
    await userEvent.selectOptions(screen.getByLabelText('Block 1 target'), 'Level');
    const v = screen.getByLabelText('Block 1 level value');
    await userEvent.clear(v);
    await userEvent.type(v, '6');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(stub.bodies('POST /api/workouts')).toEqual([{ name: 'Mine', type: 'interval', blocks: [{ duration_s: 120, level: 6 }] }]));
    // Saving a new workout opens it, so the Ride link appears.
    expect(await screen.findByRole('link', { name: /Ride this workout/ })).toHaveAttribute('href', '/?workout=9');
  });

  it('disables Save while a block has no target', async () => {
    base();
    renderWithClient(ui, '/workouts/new');
    await userEvent.type(await screen.findByLabelText('Name'), 'Mine');
    const save = screen.getByRole('button', { name: 'Save' });
    expect(save).toBeEnabled();
    await userEvent.clear(screen.getByLabelText('Block 1 offset value'));
    expect(save).toBeDisabled();
    expect(screen.getByText('Set a level or an offset.')).toBeInTheDocument();
  });

  it('disables Save without a name', async () => {
    base();
    renderWithClient(ui, '/workouts/new');
    expect(await screen.findByRole('button', { name: 'Save' })).toBeDisabled();
  });

  it('keeps durations when the type is switched', async () => {
    base();
    renderWithClient(ui, '/workouts/7');
    expect(await screen.findByLabelText('Block 2 duration')).toHaveValue('2:00');
    await userEvent.click(screen.getByRole('radio', { name: 'ERG' }));
    expect(screen.getByLabelText('Block 1 duration')).toHaveValue('1:00');
    expect(screen.getByLabelText('Block 2 duration')).toHaveValue('2:00');
    expect(screen.getByLabelText('Block 2 target')).toHaveValue('ftp_pct');
    expect(screen.getByText('Total 3:00')).toBeInTheDocument();
  });

  it('saves an edit with PUT', async () => {
    const stub = base({ 'PUT /api/workouts/7': [200, workout()] });
    renderWithClient(ui, '/workouts/7');
    await userEvent.click(await screen.findByRole('button', { name: 'Save' }));
    await waitFor(() => expect(stub.bodies('PUT /api/workouts/7')).toHaveLength(1));
    expect(stub.bodies('PUT /api/workouts/7')[0]).toMatchObject({ name: 'Sweet spot', type: 'interval' });
    expect(await screen.findByText(/Saved\./)).toBeInTheDocument();
  });

  it('offers Ride this workout', async () => {
    base();
    renderWithClient(ui, '/workouts/7');
    expect(await screen.findByRole('link', { name: /Ride this workout/ })).toHaveAttribute('href', '/?workout=7');
  });

  it('asks twice to delete and explains a 409 in use', async () => {
    const stub = base({ 'DELETE /api/workouts/7': apiError(409, 'workout_in_use', 'busy') });
    renderWithClient(ui, '/workouts/7');
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    expect(stub.calls.some((c) => c.key === 'DELETE /api/workouts/7')).toBe(false);
    await userEvent.click(screen.getByRole('button', { name: 'Tap again to delete' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/in use/);
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });

  it('deletes and returns to the list', async () => {
    base({ 'DELETE /api/workouts/7': [204] });
    renderWithClient(ui, '/workouts/7');
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    await userEvent.click(screen.getByRole('button', { name: 'Tap again to delete' }));
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Workouts' })).toBeInTheDocument());
  });

  it('is read-only for someone who neither owns it nor administers', async () => {
    base({ 'GET /api/workouts/7': [200, workout({ owner_id: 5 })] });
    renderWithClient(ui, '/workouts/7');
    expect(await screen.findByText(/Only the owner or an admin/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Ride this workout/ })).toBeInTheDocument();
  });

  it('lets an admin edit anyone\'s workout', async () => {
    base({ 'GET /api/auth/me': me({ is_admin: true }), 'GET /api/workouts/7': [200, workout({ owner_id: 5 })] });
    renderWithClient(ui, '/workouts/7');
    expect(await screen.findByRole('button', { name: 'Delete' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled();
  });

  it('previews for the chosen rider', async () => {
    base({ 'GET /api/riders': [200, [rider(), rider({ id: 2, name: 'Bo', ftp_w: 300 })]], 'GET /api/workouts/7': [200, workout({ type: 'erg', blocks: [{ duration_s: 60, ftp_pct: 50 }] })] });
    renderWithClient(ui, '/workouts/7');
    expect(await screen.findByText('100 W', { selector: 'title' })).toBeInTheDocument();
    await userEvent.selectOptions(await screen.findByLabelText('Preview for rider'), 'Bo');
    expect(await screen.findByText('150 W', { selector: 'title' })).toBeInTheDocument();
  });

  it('shows a gone message for a missing workout', async () => {
    base({ 'GET /api/workouts/7': apiError(404, 'not_found', 'no') });
    renderWithClient(ui, '/workouts/7');
    expect(await screen.findByText(/no longer exists/)).toBeInTheDocument();
  });
});
