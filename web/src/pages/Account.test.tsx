import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { AccountPage } from './Account';
import { apiError, renderWithClient, stubApi } from '@/test-utils';

const me = (display_name: string) => ({
  user: { id: 1, username: 'ann', display_name, is_admin: false },
  scopes: ['read', 'control'],
  via: 'session',
});

afterEach(() => vi.unstubAllGlobals());

describe('AccountPage', () => {
  it('saves the display name', async () => {
    const api = stubApi({
      'GET /api/auth/me': [200, me('Ann')],
      'PATCH /api/auth/me': [200, me('Ann B')],
    });
    renderWithClient(<AccountPage />);
    const input = await screen.findByLabelText('Display name');
    await vi.waitFor(() => expect(input).toHaveValue('Ann'));
    await userEvent.clear(input);
    await userEvent.type(input, '  Ann B ');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Saved.')).toBeInTheDocument();
    expect(api.bodies('PATCH /api/auth/me')).toEqual([{ display_name: 'Ann B' }]);
  });

  it('shows an error', async () => {
    stubApi({
      'GET /api/auth/me': [200, me('Ann')],
      'PATCH /api/auth/me': apiError(400, 'invalid_request', 'display_name: too long'),
    });
    renderWithClient(<AccountPage />);
    const input = await screen.findByLabelText('Display name');
    await vi.waitFor(() => expect(input).toHaveValue('Ann'));
    await userEvent.type(input, 'x');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('display_name: too long')).toBeInTheDocument();
  });
});
