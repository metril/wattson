import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { providerFixture, renderWithClient, stubApi } from '@/test-utils';
import { ProvidersCard } from './ProvidersCard';

describe('ProvidersCard', () => {
  it('lists a provider with a copyable redirect URI', async () => {
    stubApi({ 'GET /api/admin/oidc-providers': [200, [providerFixture()]] });
    const user = userEvent.setup();
    const write = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
    renderWithClient(<ProvidersCard />);
    expect(await screen.findByText('https://w.example/api/auth/oidc/authentik/callback')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /copy/i }));
    expect(write).toHaveBeenCalledWith('https://w.example/api/auth/oidc/authentik/callback');
    expect(await screen.findByText('Copied')).toBeInTheDocument();
  });

  it('adds a provider through the form and refreshes the list', async () => {
    let list = [] as ReturnType<typeof providerFixture>[];
    stubApi({
      'GET /api/admin/oidc-providers': () => [200, list],
      'POST /api/admin/oidc-providers': () => {
        list = [providerFixture()];
        return [201, list[0]];
      },
    });
    renderWithClient(<ProvidersCard />);
    await userEvent.click(await screen.findByRole('button', { name: /add provider/i }));
    await userEvent.type(screen.getByLabelText('Display name'), 'Authentik');
    await userEvent.type(screen.getByLabelText('Discovery URL'), 'https://a/');
    await userEvent.type(screen.getByLabelText('Client ID'), 'w');
    await userEvent.type(screen.getByLabelText('Client secret'), 's');
    await userEvent.click(screen.getByRole('button', { name: /create provider/i }));
    expect(await screen.findByText('https://w.example/api/auth/oidc/authentik/callback')).toBeInTheDocument();
    expect(screen.queryByLabelText('Display name')).toBeNull();
  });

  it('deletes only after confirmation', async () => {
    const api = stubApi({
      'GET /api/admin/oidc-providers': [200, [providerFixture()]],
      'DELETE /api/admin/oidc-providers/1': [204],
    });
    renderWithClient(<ProvidersCard />);
    await userEvent.click(await screen.findByRole('button', { name: /delete authentik/i }));
    expect(api.bodies('DELETE /api/admin/oidc-providers/1')).toHaveLength(0);
    await userEvent.click(screen.getByRole('button', { name: /confirm delete/i }));
    await waitFor(() => expect(api.bodies('DELETE /api/admin/oidc-providers/1')).toHaveLength(1));
  });

});
