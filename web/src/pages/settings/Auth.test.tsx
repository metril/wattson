import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithClient, settingsFixture, stubApi } from '@/test-utils';
import { AuthPage } from './Auth';

describe('AuthPage', () => {
  it('saves trusted proxies as an array and explains reverse proxies', async () => {
    const s = settingsFixture();
    const api = stubApi({
      'GET /api/settings': [200, s],
      'PATCH /api/settings': [200, s],
      'GET /api/admin/oidc-providers': [200, []],
    });
    renderWithClient(<AuthPage />);
    expect(await screen.findByText(/setup admin can always sign in locally/i)).toBeInTheDocument();
    expect(screen.getByText(/X-Forwarded-For/)).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText('Trusted proxies'), '172.18.0.0/16, 10.0.0.1{Enter}192.168.0.0/24');
    await userEvent.click(screen.getByRole('button', { name: /save/i }));
    await waitFor(() => expect(api.bodies('PATCH /api/settings')).toHaveLength(1));
    expect(api.bodies('PATCH /api/settings')[0]).toEqual({
      auth: { trusted_proxies: ['172.18.0.0/16', '10.0.0.1', '192.168.0.0/24'] },
    });
  });

  it('shows the provider list', async () => {
    stubApi({
      'GET /api/settings': [200, settingsFixture()],
      'GET /api/admin/oidc-providers': [200, []],
    });
    renderWithClient(<AuthPage />);
    expect(await screen.findByRole('button', { name: /add provider/i })).toBeInTheDocument();
  });
});
