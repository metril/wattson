import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { apiError, providerFixture, renderWithClient, settingsFixture, stubApi } from '@/test-utils';
import { ProviderForm } from './ProviderForm';

async function fillCreate() {
  await userEvent.type(screen.getByLabelText('Display name'), 'Authentik Home');
  await userEvent.type(screen.getByLabelText('Discovery URL'), 'https://auth.example/application/o/w/');
  await userEvent.type(screen.getByLabelText('Client ID'), 'wattson');
  await userEvent.type(screen.getByLabelText('Client secret'), 's3cret');
}

describe('ProviderForm', () => {
  it('derives the slug from the name until it is edited', async () => {
    stubApi({});
    renderWithClient(<ProviderForm onDone={() => {}} />);
    const slug = screen.getByLabelText('Slug');
    await userEvent.type(screen.getByLabelText('Display name'), 'Authentik Home');
    expect(slug).toHaveValue('authentik-home');
    await userEvent.clear(slug);
    await userEvent.type(slug, 'sso');
    await userEvent.type(screen.getByLabelText('Display name'), ' 2');
    expect(slug).toHaveValue('sso');
  });

  it('creates with the secret and comma lists as arrays', async () => {
    const api = stubApi({ 'POST /api/admin/oidc-providers': [201, providerFixture()] });
    const onDone = vi.fn();
    renderWithClient(<ProviderForm onDone={onDone} />);
    await fillCreate();
    await userEvent.type(screen.getByLabelText('Allowed groups'), 'riders, admins');
    await userEvent.type(screen.getByLabelText('Allowed emails'), 'a@x.io,  b@x.io');
    await userEvent.click(screen.getByRole('switch', { name: /auto-create users/i }));
    await userEvent.click(screen.getByRole('button', { name: /create provider/i }));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(api.bodies('POST /api/admin/oidc-providers')[0]).toEqual({
      display_name: 'Authentik Home',
      slug: 'authentik-home',
      discovery_url: 'https://auth.example/application/o/w/',
      client_id: 'wattson',
      client_secret: 's3cret',
      scopes: 'openid profile email groups',
      groups_claim: 'groups',
      allowed_groups: ['riders', 'admins'],
      allowed_emails: ['a@x.io', 'b@x.io'],
      admin_group: '',
      auto_create_users: true,
      link_by_email: false,
      enabled: true,
    });
  });

  it('requires a client secret on create', async () => {
    const api = stubApi({});
    renderWithClient(<ProviderForm onDone={() => {}} />);
    await userEvent.type(screen.getByLabelText('Display name'), 'X');
    await userEvent.click(screen.getByRole('button', { name: /create provider/i }));
    expect(await screen.findByText(/client secret is required/i)).toBeInTheDocument();
    expect(api.calls.filter((c) => c.key.startsWith('POST'))).toHaveLength(0);
  });

  it('edit without touching the secret sends only changed fields and no client_secret', async () => {
    const api = stubApi({ 'PATCH /api/admin/oidc-providers/1': [200, providerFixture()] });
    const onDone = vi.fn();
    renderWithClient(<ProviderForm initial={providerFixture()} onDone={onDone} />);
    expect(screen.getByLabelText('Client secret')).toHaveValue('');
    expect(screen.getByLabelText('Slug')).toBeDisabled();
    await userEvent.clear(screen.getByLabelText('Display name'));
    await userEvent.type(screen.getByLabelText('Display name'), 'Home SSO');
    await userEvent.click(screen.getByRole('button', { name: /save provider/i }));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    const body = api.bodies('PATCH /api/admin/oidc-providers/1')[0];
    expect(body).toEqual({ display_name: 'Home SSO' });
    expect(body).not.toHaveProperty('client_secret');
  });

  it('shows the redirect URI read-only with a hint for existing providers', () => {
    stubApi({});
    renderWithClient(<ProviderForm initial={providerFixture()} onDone={() => {}} />);
    expect(screen.getByText('https://w.example/api/auth/oidc/authentik/callback')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /copy redirect uri/i })).toBeInTheDocument();
  });

  it('slug_taken shows under the slug field', async () => {
    stubApi({ 'POST /api/admin/oidc-providers': apiError(409, 'slug_taken') });
    renderWithClient(<ProviderForm onDone={() => {}} />);
    await fillCreate();
    await userEvent.click(screen.getByRole('button', { name: /create provider/i }));
    expect(await screen.findByText('Slug already in use')).toBeInTheDocument();
    expect(screen.getByLabelText('Slug')).toHaveAttribute('aria-invalid', 'true');
  });

  it('checks discovery with the typed URL', async () => {
    const api = stubApi({
      'POST /api/admin/oidc-providers/discover': [200, { ok: true, message: 'issuer https://auth.example', latency_ms: 20 }],
    });
    renderWithClient(<ProviderForm onDone={() => {}} />);
    await userEvent.type(screen.getByLabelText('Discovery URL'), 'https://auth.example/');
    await userEvent.click(screen.getByRole('button', { name: /check discovery/i }));
    expect(await screen.findByRole('status')).toHaveTextContent('issuer https://auth.example');
    expect(api.bodies('POST /api/admin/oidc-providers/discover')[0]).toEqual({ discovery_url: 'https://auth.example/' });
  });

  it('previews the redirect URI from the public URL and slug while creating', async () => {
    stubApi({ 'GET /api/settings': [200, settingsFixture()] });
    renderWithClient(<ProviderForm onDone={() => {}} />);
    await userEvent.type(screen.getByLabelText('Display name'), 'Authentik Home');
    expect(
      await screen.findByText('https://w.example/api/auth/oidc/authentik-home/callback'),
    ).toBeInTheDocument();
  });

  it('explains that link-by-email skips admin accounts', () => {
    stubApi({});
    renderWithClient(<ProviderForm onDone={() => {}} />);
    expect(screen.getByText(/admin accounts.*never linked/i)).toBeInTheDocument();
  });
});
