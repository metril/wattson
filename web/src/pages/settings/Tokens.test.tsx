import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { TokensPage } from './Tokens';
import { apiError, renderWithClient, stubApi, userFixture } from '@/test-utils';

const qrFail = vi.hoisted(() => ({ on: false }));
vi.mock('qrcode', async (orig) => {
  const m = await orig<typeof import('qrcode')>();
  return { ...m, toDataURL: (...a: Parameters<typeof m.toDataURL>) => (qrFail.on ? Promise.reject(new Error('boom')) : (m.toDataURL as (...x: unknown[]) => Promise<string>)(...a)) };
});

const meOut = (admin: boolean, via = 'session', scopes = admin ? ['read', 'control', 'admin'] : ['read', 'control']) =>
  [200, { user: userFixture({ is_admin: admin }), scopes, via }] as [number, unknown];

const tok = (over = {}) => ({
  id: 7,
  name: 'old',
  prefix: 'wat_oldprefix',
  scopes: ['read'],
  created_at: '2026-01-01T00:00:00Z',
  last_used_at: null,
  ...over,
});

function setup(admin = false) {
  const list = [tok()];
  const stub = stubApi({
    'GET /api/auth/me': meOut(admin),
    'GET /api/tokens': () => [200, list],
    'POST /api/tokens': (b) => {
      const t = tok({ id: 8, name: b.name, prefix: 'wat_newprefix', scopes: b.scopes });
      list.push(t);
      return [201, { ...t, token: 'wat_secretvalue123' }];
    },
    'DELETE /api/tokens/7': () => [204],
    'DELETE /api/tokens/8': () => [204],
    'GET /api/settings': () => [200, { general: { public_url: 'https://w.example' } }],
  });
  return { stub, list };
}

afterEach(() => {
  qrFail.on = false;
  vi.unstubAllGlobals();
});

describe('TokensPage', () => {
  it('shows the plaintext once, copies it, then only the prefix remains', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const { stub } = setup();
    renderWithClient(<TokensPage />);
    await screen.findByText('wat_oldprefix…');
    await userEvent.type(screen.getByLabelText('Name'), 'script');
    await userEvent.click(screen.getByRole('button', { name: 'Create token' }));
    expect(await screen.findByText('wat_secretvalue123')).toBeInTheDocument();
    expect(screen.getByText(/shown once/i)).toBeInTheDocument();
    expect(stub.bodies('POST /api/tokens')[0]).toEqual({ name: 'script', scopes: ['read'] });
    await userEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(writeText).toHaveBeenCalledWith('wat_secretvalue123');
    await userEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(screen.queryByText('wat_secretvalue123')).not.toBeInTheDocument();
    expect(await screen.findByText('wat_newprefix…')).toBeInTheDocument();
  });

  it('pairs a display with a QR code and link', async () => {
    const { stub } = setup();
    renderWithClient(<TokensPage />);
    await screen.findByText('wat_oldprefix…');
    await userEvent.click(screen.getByRole('button', { name: 'Pair a display' }));
    const img = await screen.findByAltText('Pairing QR code');
    expect(img.getAttribute('src')).toMatch(/^data:image\/png/);
    expect(await screen.findByText('http://localhost:3000/kiosk#token=wat_secretvalue123')).toBeInTheDocument();
    const body = stub.bodies('POST /api/tokens')[0] as { name: string; scopes: string[] };
    expect(body.scopes).toEqual(['read', 'control']);
    expect(body.name).toMatch(/^Kiosk \d{4}-\d{2}-\d{2} \d{2}:\d{2}$/);
  });

  it('uses the public URL for admins', async () => {
    setup(true);
    renderWithClient(<TokensPage />);
    await screen.findByText('wat_oldprefix…');
    await userEvent.click(screen.getByRole('button', { name: 'Pair a display' }));
    expect(await screen.findByText('https://w.example/kiosk#token=wat_secretvalue123')).toBeInTheDocument();
  });

  it('offers the admin scope to admins only', async () => {
    setup(false);
    const a = renderWithClient(<TokensPage />);
    await screen.findByText('wat_oldprefix…');
    expect(screen.queryByRole('checkbox', { name: /admin/i })).not.toBeInTheDocument();
    a.unmount();
    setup(true);
    renderWithClient(<TokensPage />);
    expect(await screen.findByRole('checkbox', { name: /admin/i })).toBeInTheDocument();
  });

  it('revokes after a second click', async () => {
    const { stub } = setup();
    renderWithClient(<TokensPage />);
    await userEvent.click(await screen.findByRole('button', { name: 'Revoke old' }));
    expect(stub.bodies('DELETE /api/tokens/7')).toHaveLength(0);
    await userEvent.click(screen.getByRole('button', { name: /click again to confirm/i }));
    await waitFor(() => expect(stub.bodies('DELETE /api/tokens/7')).toHaveLength(1));
  });

  it('shows an error when revoke is refused', async () => {
    stubApi({
      'GET /api/auth/me': meOut(false),
      'GET /api/tokens': () => [200, [tok()]],
      'DELETE /api/tokens/7': () => apiError(403, 'insufficient_scope', 'needs control'),
    });
    renderWithClient(<TokensPage />);
    await userEvent.click(await screen.findByRole('button', { name: 'Revoke old' }));
    await userEvent.click(screen.getByRole('button', { name: /click again to confirm/i }));
    expect(await screen.findByText('needs control')).toBeInTheDocument();
  });

  it('hides Revoke without the control scope', async () => {
    stubApi({
      'GET /api/auth/me': meOut(false, 'session', ['read']),
      'GET /api/tokens': () => [200, [tok()]],
    });
    renderWithClient(<TokensPage />);
    await screen.findByText('wat_oldprefix…');
    expect(screen.getByRole('button', { name: 'Pair a display' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^Revoke/ })).not.toBeInTheDocument();
  });

  it('tells token callers to sign in with an account', async () => {
    stubApi({
      'GET /api/auth/me': meOut(false, 'token'),
      'GET /api/tokens': () => [200, [tok()]],
    });
    renderWithClient(<TokensPage />);
    expect(await screen.findByText('Sign in with an account to manage tokens.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Pair a display' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Create token' })).not.toBeInTheDocument();
  });

  it('deletes the token again when the QR cannot be made', async () => {
    const stub = setup().stub;
    qrFail.on = true;
    renderWithClient(<TokensPage />);
    await screen.findByText('wat_oldprefix…');
    await userEvent.click(screen.getByRole('button', { name: 'Pair a display' }));
    expect(await screen.findByText('boom')).toBeInTheDocument();
    expect(stub.calls.some((c) => c.key === 'DELETE /api/tokens/8')).toBe(true);
  });
});
