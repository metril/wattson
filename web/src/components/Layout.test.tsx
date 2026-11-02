import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useLocation } from 'react-router';
import { AppRoutes } from '@/App';

function Probe() {
  return <div data-testid="loc">{useLocation().pathname}</div>;
}

const user = { id: 1, username: 'a', display_name: 'A', email: '', is_admin: false, is_bootstrap: false, disabled: false, oidc: false, created_at: '2026-01-01T00:00:00Z' };

function stub(logout: () => Response | Promise<Response>) {
  const json = (s: number, b: unknown) =>
    new Response(JSON.stringify(b), { status: s, headers: { 'content-type': 'application/json' } });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === '/api/auth/providers') return json(200, { needs_setup: false, local_login: true, providers: [] });
      if (path === '/api/auth/me') return json(200, { user, scopes: ['read'], via: 'session' });
      if (path === '/api/auth/logout') return logout();
      return json(404, { error: { code: 'not_found', message: 'x' } });
    }),
  );
}

async function signOut() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/riders']}>
        <Probe />
        <AppRoutes />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await userEvent.click(await screen.findByRole('button', { name: /sign out/i }));
}

afterEach(() => vi.unstubAllGlobals());

describe('Layout sign out', () => {
  it('navigates to /login after a successful logout', async () => {
    stub(() => new Response(null, { status: 204 }));
    await signOut();
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument();
  });

  it('keeps the session and shows an error when logout returns non-2xx', async () => {
    stub(() =>
      new Response(JSON.stringify({ error: { code: 'internal', message: 'db down' } }), {
        status: 500,
        headers: { 'content-type': 'application/json' },
      }),
    );
    await signOut();
    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't sign out: db down");
    expect(screen.getByTestId('loc')).toHaveTextContent('/riders');
  });

  it('keeps the session and shows an error on network failure', async () => {
    stub(() => {
      throw new TypeError('network down');
    });
    await signOut();
    expect(await screen.findByRole('alert')).toHaveTextContent('You are still signed in');
    expect(screen.getByTestId('loc')).toHaveTextContent('/riders');
  });
});
