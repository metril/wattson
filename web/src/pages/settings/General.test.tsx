import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithClient, settingsFixture, stubApi } from '@/test-utils';
import { GeneralForm } from './General';

describe('GeneralForm', () => {
  it('PATCHes public_url and session TTL into their own sections', async () => {
    const s = settingsFixture();
    const api = stubApi({ 'GET /api/settings': [200, s], 'PATCH /api/settings': [200, s] });
    renderWithClient(<GeneralForm />);
    const url = await screen.findByLabelText('Public URL');
    expect(url).toHaveValue('https://w.example');
    await userEvent.clear(url);
    await userEvent.type(url, 'https://wattson.home');
    const ttl = screen.getByLabelText(/session lifetime/i);
    await userEvent.clear(ttl);
    await userEvent.type(ttl, '24');
    await userEvent.click(screen.getByRole('switch', { name: /allow local login/i }));
    await userEvent.click(screen.getByRole('button', { name: /save/i }));
    await waitFor(() => expect(api.bodies('PATCH /api/settings')).toHaveLength(1));
    expect(api.bodies('PATCH /api/settings')[0]).toEqual({
      general: { public_url: 'https://wattson.home' },
      auth: { session_ttl_hours: 24, allow_local_login: false },
    });
  });
});
