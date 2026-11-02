import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { apiError, renderWithClient, stubApi, userFixture } from '@/test-utils';
import { UsersPage } from './Users';

const root = userFixture({ id: 1, username: 'root', display_name: 'Root', is_admin: true, is_bootstrap: true });
const bob = userFixture({ id: 2, username: 'bob', is_admin: true });
const amy = userFixture({ id: 3, username: 'amy', display_name: 'Amy', oidc: true });

describe('UsersPage', () => {
  it('lists users and locks the bootstrap row', async () => {
    stubApi({ 'GET /api/users': [200, [root, bob, amy]] });
    renderWithClient(<UsersPage />);
    expect(await screen.findByText('amy')).toBeInTheDocument();
    expect(screen.getByRole('switch', { name: 'Admin: root' })).toBeDisabled();
    expect(screen.getByRole('switch', { name: 'Disabled: root' })).toBeDisabled();
    expect(screen.getByRole('switch', { name: 'Admin: bob' })).toBeEnabled();
    expect(screen.getByText('OIDC')).toBeInTheDocument();
    expect(screen.getAllByText('Local')).toHaveLength(2);
  });

  it('PATCHes the admin flag and updates the row', async () => {
    const api = stubApi({
      'GET /api/users': [200, [root, bob, amy]],
      'PATCH /api/users/3': [200, { ...amy, is_admin: true }],
    });
    renderWithClient(<UsersPage />);
    const toggle = await screen.findByRole('switch', { name: 'Admin: amy' });
    await userEvent.click(toggle);
    await waitFor(() => expect(screen.getByRole('switch', { name: 'Admin: amy' })).toBeChecked());
    expect(api.bodies('PATCH /api/users/3')[0]).toEqual({ is_admin: true });
  });

  it('shows the last_admin refusal inline and leaves the toggle as it was', async () => {
    stubApi({
      'GET /api/users': [200, [{ ...root, is_bootstrap: false, is_admin: false }, bob]],
      'PATCH /api/users/2': apiError(409, 'last_admin'),
    });
    renderWithClient(<UsersPage />);
    await userEvent.click(await screen.findByRole('switch', { name: 'Admin: bob' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('At least one admin must remain');
    expect(screen.getByRole('switch', { name: 'Admin: bob' })).toBeChecked();
  });

  it('shows the bootstrap_protected refusal', async () => {
    stubApi({
      'GET /api/users': [200, [bob]],
      'PATCH /api/users/2': apiError(409, 'bootstrap_protected'),
    });
    renderWithClient(<UsersPage />);
    await userEvent.click(await screen.findByRole('switch', { name: 'Disabled: bob' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/setup admin/i);
  });
});
