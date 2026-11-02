import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useLocation } from 'react-router';
import { LoginPage, formatRetry } from './Login';
import { stubApi } from '@/test-utils';

function Probe() {
  return <div data-testid="loc">{useLocation().pathname}</div>;
}

function renderLogin(route: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[route]}>
        <Probe />
        <LoginPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const info = (over = {}) => [
  200,
  {
    needs_setup: false,
    local_login: true,
    providers: [{ slug: 'authentik', display_name: 'Authentik', login_url: '/api/auth/oidc/authentik/login' }],
    ...over,
  },
] as [number, unknown];

async function signIn() {
  await userEvent.type(await screen.findByLabelText('Username'), 'admin');
  await userEvent.type(screen.getByLabelText('Password'), 'pw-pw-pw-pw-1');
  await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));
}

afterEach(() => vi.unstubAllGlobals());

describe('LoginPage', () => {
  it('links each provider with the return_to', async () => {
    stubApi({ 'GET /api/auth/providers': info() });
    renderLogin('/login?return_to=/riders');
    const a = await screen.findByRole('link', { name: /Authentik/ });
    expect(a).toHaveAttribute('href', '/api/auth/oidc/authentik/login?return_to=%2Friders');
  });

  it('shows the friendly message for an OIDC callback error', async () => {
    stubApi({ 'GET /api/auth/providers': info() });
    renderLogin('/login?error=forbidden');
    expect(await screen.findByRole('alert')).toHaveTextContent('Your account is not in an allowed group.');
  });

  it('maps not_provisioned', async () => {
    stubApi({ 'GET /api/auth/providers': info() });
    renderLogin('/login?error=not_provisioned');
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'No Wattson account is linked to this identity. Ask an admin.',
    );
  });

  it('navigates to return_to after a local login', async () => {
    const stub = stubApi({ 'GET /api/auth/providers': info(), 'POST /api/auth/login': [200, {}] });
    renderLogin('/login?return_to=/riders');
    await signIn();
    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe('/riders'));
    expect(stub.bodies('POST /api/auth/login')[0]).toEqual({ username: 'admin', password: 'pw-pw-pw-pw-1' });
  });

  it('ignores a protocol-relative return_to', async () => {
    stubApi({ 'GET /api/auth/providers': info(), 'POST /api/auth/login': [200, {}] });
    renderLogin('/login?return_to=//evil.example');
    await signIn();
    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe('/'));
  });

  it('shows a retry countdown on 429', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (req: Request) => {
        const p = new URL(req.url).pathname;
        if (p === '/api/auth/providers') {
          return new Response(JSON.stringify(info()[1]), { status: 200, headers: { 'content-type': 'application/json' } });
        }
        return new Response(JSON.stringify({ error: { code: 'rate_limited', message: 'slow down' } }), {
          status: 429,
          headers: { 'content-type': 'application/json', 'Retry-After': '600' },
        });
      }),
    );
    renderLogin('/login');
    await signIn();
    expect(await screen.findByRole('alert')).toHaveTextContent('Too many attempts. Try again in 10 minutes');
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeDisabled();
  });

  it('explains a disabled local login', async () => {
    stubApi({
      'GET /api/auth/providers': info({ local_login: false }),
      'POST /api/auth/login': [403, { error: { code: 'local_login_disabled', message: 'x' } }],
    });
    renderLogin('/login');
    await userEvent.click(await screen.findByRole('button', { name: 'Sign in with a local account' }));
    await signIn();
    expect(await screen.findByRole('alert')).toHaveTextContent('Local login is disabled. Use a provider button.');
  });

  it('hides the local form behind a toggle when local login is off', async () => {
    stubApi({ 'GET /api/auth/providers': info({ local_login: false }) });
    renderLogin('/login');
    await screen.findByRole('link', { name: /Authentik/ });
    expect(screen.queryByLabelText('Username')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Sign in with a local account' }));
    expect(screen.getByLabelText('Username')).toBeInTheDocument();
  });
});

describe('formatRetry', () => {
  it('rounds up to minutes, then counts seconds', () => {
    expect(formatRetry(600)).toBe('10 minutes');
    expect(formatRetry(61)).toBe('2 minutes');
    expect(formatRetry(60)).toBe('1 minute');
    expect(formatRetry(45)).toBe('45 seconds');
    expect(formatRetry(1)).toBe('1 second');
  });
});
