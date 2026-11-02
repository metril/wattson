import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { apiError, renderWithClient, settingsFixture, stubApi } from '@/test-utils';
import { IntegrationsForm } from './Integrations';

describe('IntegrationsForm', () => {
  it('PATCHes only the changed field, never the saved token', async () => {
    const s = settingsFixture();
    const api = stubApi({
      'GET /api/settings': [200, s],
      'PATCH /api/settings': [200, s],
    });
    renderWithClient(<IntegrationsForm />);
    const url = await screen.findByLabelText('Home Assistant URL');
    expect(screen.getByLabelText('Long-lived access token')).toHaveValue('');
    await userEvent.clear(url);
    await userEvent.type(url, 'http://ha.local:8123');
    await userEvent.click(screen.getByRole('button', { name: /save/i }));
    await waitFor(() => expect(api.bodies('PATCH /api/settings')).toHaveLength(1));
    expect(api.bodies('PATCH /api/settings')[0]).toEqual({ ha: { url: 'http://ha.local:8123' } });
  });

  it('Clear sends an empty token string', async () => {
    const s = settingsFixture();
    const api = stubApi({ 'GET /api/settings': [200, s], 'PATCH /api/settings': [200, s] });
    renderWithClient(<IntegrationsForm />);
    await screen.findByLabelText('Home Assistant URL');
    await userEvent.click(screen.getByRole('button', { name: /clear long-lived access token/i }));
    await userEvent.click(screen.getByRole('button', { name: /save/i }));
    await waitFor(() => expect(api.bodies('PATCH /api/settings')).toHaveLength(1));
    expect(api.bodies('PATCH /api/settings')[0]).toEqual({ ha: { token: '' } });
  });

  it('tests HA with the typed URL and token, and explains the stored-token rule', async () => {
    const api = stubApi({
      'GET /api/settings': [200, settingsFixture()],
      'POST /api/settings/test/ha': [200, { ok: true, message: 'HA 2026.1', latency_ms: 30 }],
    });
    renderWithClient(<IntegrationsForm />);
    const url = await screen.findByLabelText('Home Assistant URL');
    expect(screen.getByText(/enter the token to test a different url/i)).toBeInTheDocument();
    await userEvent.clear(url);
    await userEvent.type(url, 'http://other:8123');
    await userEvent.type(screen.getByLabelText('Long-lived access token'), 'tok');
    await userEvent.click(screen.getByRole('button', { name: /test home assistant/i }));
    expect(await screen.findByRole('status')).toHaveTextContent('HA 2026.1');
    expect(api.bodies('POST /api/settings/test/ha')[0]).toEqual({ url: 'http://other:8123', token: 'tok' });
  });

  it('shows invalid_setting under the matching field', async () => {
    stubApi({
      'GET /api/settings': [200, settingsFixture()],
      'PATCH /api/settings': apiError(400, 'invalid_setting', 'mqtt.url: must not contain credentials'),
    });
    renderWithClient(<IntegrationsForm />);
    await userEvent.type(await screen.findByLabelText('MQTT broker URL'), 'mqtt://u:p@b');
    await userEvent.click(screen.getByRole('button', { name: /save/i }));
    expect(await screen.findByText('must not contain credentials')).toBeInTheDocument();
    expect(screen.getByLabelText('MQTT broker URL')).toHaveAttribute('aria-invalid', 'true');
  });
});
