import { act, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { AppRoutes } from '@/App';
import { renderWithClient, stubApi } from '@/test-utils';
import { bikeFixture, FakeWebSocket, tickFixture } from '@/test-ws';

const user = { id: 1, username: 'a', display_name: 'A', email: '', is_admin: false, is_bootstrap: false, disabled: false, oidc: false, created_at: '2026-01-01T00:00:00Z' };

beforeEach(() => {
  FakeWebSocket.reset();
  vi.stubGlobal('WebSocket', FakeWebSocket);
  stubApi({
    'GET /api/auth/providers': [200, { needs_setup: false, local_login: true, providers: [] }],
    'GET /api/auth/me': [200, { user, scopes: ['read', 'control'], via: 'session' }],
    'GET /api/riders': [200, []],
    'GET /api/rides': [200, { rides: [], next_cursor: null }],
  });
});
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

/** Open History, then go to the ride screen through the nav bar (Layout's key handler registered first). */
async function rideInApp() {
  renderWithClient(<AppRoutes />, '/history');
  await userEvent.click(await screen.findByRole('link', { name: 'Ride' }));
  const ws = FakeWebSocket.last;
  act(() => {
    ws.open();
    ws.receive({ type: 'state', bike: bikeFixture({ state: 'riding' }), tick: tickFixture({ state: 'riding' }) });
  });
  await screen.findByRole('button', { name: 'Pause' });
  return ws;
}

describe('ride screen inside the app shell', () => {
  it('Up/Down tune the level from the page and from the ride control buttons, after in-app navigation', async () => {
    const ws = await rideInApp();
    (document.activeElement as HTMLElement | null)?.blur();
    await userEvent.keyboard('{ArrowUp}');
    expect(ws.commands.at(-1)).toMatchObject({ command: 'level_delta', value: 1 });
    screen.getByRole('button', { name: 'Pause' }).focus();
    await userEvent.keyboard('{ArrowDown}');
    expect(ws.commands.at(-1)).toMatchObject({ command: 'level_delta', value: -1 });
    expect(screen.getByRole('button', { name: 'Pause' })).toHaveFocus();
  });

  it('arrows leave the ride for the nav bar and reach Stats, Account, Tokens and Sign out in the More menu', async () => {
    await rideInApp();
    await userEvent.click(screen.getByRole('button', { name: 'More' }));
    const more = screen.getByRole('button', { name: 'More' });
    more.focus();
    await userEvent.keyboard('{ArrowRight}');
    expect(screen.getByRole('link', { name: 'Stats' })).toHaveFocus();    await userEvent.keyboard('{ArrowRight}');
    expect(screen.getByRole('link', { name: 'Account' })).toHaveFocus();
    await userEvent.keyboard('{ArrowRight}');
    expect(screen.getByRole('link', { name: 'Tokens' })).toHaveFocus();
    await userEvent.keyboard('{ArrowRight}');
    expect(screen.getByRole('button', { name: /sign out/i })).toHaveFocus();
    await userEvent.keyboard('{ArrowLeft}');
    expect(screen.getByRole('link', { name: 'Tokens' })).toHaveFocus();
  });
});
