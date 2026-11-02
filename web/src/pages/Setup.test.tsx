import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useLocation } from 'react-router';
import { SetupPage } from './Setup';
import { apiError, settingsFixture, stubApi, userFixture } from '@/test-utils';

function Probe() {
  return <div data-testid="loc">{useLocation().pathname}</div>;
}

function renderSetup() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/setup']}>
        <Probe />
        <SetupPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

async function fillAdmin(confirm = 'correct-horse-1') {
  await userEvent.clear(screen.getByLabelText('Username'));
  await userEvent.type(screen.getByLabelText('Username'), 'admin');
  await userEvent.type(screen.getByLabelText('Password'), 'correct-horse-1');
  await userEvent.type(screen.getByLabelText('Confirm password'), confirm);
}

afterEach(() => vi.unstubAllGlobals());

describe('SetupPage', () => {
  it('blocks Next when the passwords differ or are too short', async () => {
    const stub = stubApi({});
    renderSetup();
    await fillAdmin('something-else-1');
    expect(screen.getByText("Passwords don't match.")).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
    expect(stub.bodies('POST /api/setup')).toHaveLength(0);
  });

  it('requires at least 10 characters', async () => {
    renderSetup();
    await userEvent.type(screen.getByLabelText('Username'), 'admin');
    await userEvent.type(screen.getByLabelText('Password'), 'short');
    await userEvent.type(screen.getByLabelText('Confirm password'), 'short');
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
    expect(screen.getByText(/at least 10 characters/i)).toBeInTheDocument();
  });

  it('creates the admin, saves the public URL, and finishes with two skips', async () => {
    const stub = stubApi({
      'POST /api/setup': [201, userFixture({ is_admin: true })],
      'PATCH /api/settings': [200, settingsFixture()],
      'GET /api/settings': [200, settingsFixture()],
    });
    renderSetup();
    await fillAdmin();
    await userEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(stub.bodies('POST /api/setup')[0]).toEqual({ username: 'admin', password: 'correct-horse-1' });
    expect(await screen.findByLabelText('Public URL')).toHaveValue('http://localhost:3000');
    await userEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByRole('heading', { name: 'Bike connection' })).toBeInTheDocument();
    expect(stub.bodies('PATCH /api/settings')[0]).toEqual({ general: { public_url: 'http://localhost:3000' } });
    await userEvent.click(screen.getByRole('button', { name: 'Skip' }));
    expect(await screen.findByRole('heading', { name: 'Sign-in provider' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Skip' }));
    expect(await screen.findByRole('heading', { name: 'All set' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Open Wattson' }));
    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe('/'));
  });

  it('skips the public URL without saving', async () => {
    const stub = stubApi({
      'POST /api/setup': [201, userFixture({ is_admin: true })],
      'GET /api/settings': [200, settingsFixture()],
    });
    renderSetup();
    await fillAdmin();
    await userEvent.click(screen.getByRole('button', { name: 'Next' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Skip' }));
    expect(await screen.findByRole('heading', { name: 'Bike connection' })).toBeInTheDocument();
    expect(stub.bodies('PATCH /api/settings')).toHaveLength(0);
  });

  it('redirects to /login on load when an admin already exists', async () => {
    stubApi({ 'GET /api/setup/status': [200, { needs_setup: false }] });
    renderSetup();
    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe('/login'));
  });

  it('goes to /login when setup already ran', async () => {
    stubApi({ 'POST /api/setup': apiError(409, 'already_setup', 'already') });
    renderSetup();
    await fillAdmin();
    await userEvent.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe('/login'));
  });

  it('shows a server validation error on the first step', async () => {
    stubApi({ 'POST /api/setup': apiError(400, 'invalid_request', 'username: invalid') });
    renderSetup();
    await fillAdmin();
    await userEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('username: invalid');
  });
});
