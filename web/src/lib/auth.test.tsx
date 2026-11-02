import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useLocation } from 'react-router';
import { AppRoutes } from '@/App';

function Probe() {
  const l = useLocation();
  return <div data-testid="loc">{l.pathname + l.search}</div>;
}

const admin = { id: 1, username: 'a', display_name: 'A', email: '', is_admin: true, is_bootstrap: true, disabled: false, oidc: false, created_at: '2026-01-01T00:00:00Z' };

function stub(opts: { needsSetup?: boolean; me?: 'admin' | 'user' | null }) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      const json = (s: number, b: unknown) =>
        new Response(JSON.stringify(b), { status: s, headers: { 'content-type': 'application/json' } });
      if (path === '/api/auth/providers')
        return json(200, { needs_setup: !!opts.needsSetup, local_login: true, providers: [] });
      if (path === '/api/auth/me') {
        if (!opts.me) return json(401, { error: { code: 'unauthorized', message: 'no' } });
        return json(200, {
          user: { ...admin, is_admin: opts.me === 'admin' },
          scopes: ['read'],
          via: 'session',
        });
      }
      return json(404, { error: { code: 'not_found', message: 'x' } });
    }),
  );
}

function renderAt(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Probe />
        <AppRoutes />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe('RequireAuth', () => {
  it('needs_setup renders /setup', async () => {
    stub({ needsSetup: true, me: null });
    renderAt('/riders');
    expect(await screen.findByRole('heading', { name: 'Set up Wattson' })).toBeInTheDocument();
    expect(screen.getByTestId('loc')).toHaveTextContent('/setup');
  });

  it('anonymous at /riders goes to /login?return_to', async () => {
    stub({ me: null });
    renderAt('/riders');
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument();
    expect(screen.getByTestId('loc')).toHaveTextContent('/login?return_to=%2Friders');
  });

  it('non-admin at /settings/general goes to /', async () => {
    stub({ me: 'user' });
    renderAt('/settings/general');
    expect(await screen.findByRole('heading', { name: 'Ride' })).toBeInTheDocument();
    expect(screen.getByTestId('loc')).toHaveTextContent(/^\/$/);
  });

  it('admin sees the settings page', async () => {
    stub({ me: 'admin' });
    renderAt('/settings/general');
    expect(await screen.findByRole('heading', { name: 'Settings' })).toBeInTheDocument();
  });

  it('any user can open /settings/tokens', async () => {
    stub({ me: 'user' });
    renderAt('/settings/tokens');
    expect(await screen.findByRole('heading', { name: 'API tokens' })).toBeInTheDocument();
  });
});
