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

  it('PATCHes only the public fallback toggle', async () => {
    const s = settingsFixture();
    const api = stubApi({ 'GET /api/settings': [200, s], 'PATCH /api/settings': [200, s] });
    renderWithClient(<IntegrationsForm />);
    const toggle = await screen.findByRole('switch', { name: /use public brouter\.de when a region is missing/i });
    expect(screen.getByText('Sends your waypoints to brouter.de')).toBeInTheDocument();
    expect(screen.queryByLabelText('Public BRouter URL')).not.toBeInTheDocument();
    await userEvent.click(toggle);
    expect(screen.getByLabelText('Public BRouter URL')).toHaveValue('https://brouter.de');
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }));
    await waitFor(() => expect(api.bodies('PATCH /api/settings')).toHaveLength(1));
    expect(api.bodies('PATCH /api/settings')[0]).toEqual({ brouter: { public_fallback: true } });
  });

  it('PATCHes the OpenTopoData switch and links to map regions', async () => {
    const s = settingsFixture();
    const api = stubApi({ 'GET /api/settings': [200, s], 'PATCH /api/settings': [200, s] });
    renderWithClient(<IntegrationsForm />);
    const toggle = await screen.findByRole('switch', { name: /use opentopodata for missing elevation and above 60° latitude/i });
    expect(screen.getByText('Sends route coordinates to the OpenTopoData server')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Manage map regions' })).toHaveAttribute('href', '/settings/maps');
    await userEvent.click(toggle);
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }));
    await waitFor(() => expect(api.bodies('PATCH /api/settings')).toHaveLength(1));
    expect(api.bodies('PATCH /api/settings')[0]).toEqual({ elevation: { opentopodata: true } });
  });

  it('does not send a hidden field that was edited then hidden', async () => {
    const s = settingsFixture({ brouter: { public_fallback: true } });
    const api = stubApi({ 'GET /api/settings': [200, s], 'PATCH /api/settings': [200, s] });
    renderWithClient(<IntegrationsForm />);
    const url = await screen.findByLabelText('Public BRouter URL');
    await userEvent.clear(url);
    await userEvent.type(url, 'https://other.example');
    await userEvent.click(screen.getByRole('switch', { name: /use public brouter\.de/i }));
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }));
    await waitFor(() => expect(api.bodies('PATCH /api/settings')).toHaveLength(1));
    expect(api.bodies('PATCH /api/settings')[0]).toEqual({ brouter: { public_fallback: false } });
  });

  it('tests BRouter with the typed URL', async () => {
    const api = stubApi({
      'GET /api/settings': [200, settingsFixture()],
      'POST /api/settings/test/brouter': [200, { ok: true, message: 'BRouter ok', latency_ms: 12 }],
    });
    renderWithClient(<IntegrationsForm />);
    await userEvent.click(await screen.findByRole('button', { name: /test brouter/i }));
    expect(await screen.findByText(/BRouter ok/)).toBeInTheDocument();
    expect(api.bodies('POST /api/settings/test/brouter')).toEqual([{}]);
    await userEvent.type(screen.getByLabelText('BRouter URL'), 'http://brouter:17777');
    await userEvent.click(screen.getByRole('button', { name: /test brouter/i }));
    await waitFor(() => expect(api.bodies('POST /api/settings/test/brouter')).toHaveLength(2));
    expect(api.bodies('POST /api/settings/test/brouter')[1]).toEqual({ url: 'http://brouter:17777' });
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

  it('hints to clear the segments folder for a remote BRouter', async () => {
    const hint = /clear 'segments folder'/i;
    const bundled = settingsFixture({ brouter: { url: 'http://brouter:17777' } });
    stubApi({ 'GET /api/settings': [200, bundled] });
    const a = renderWithClient(<IntegrationsForm />);
    await screen.findByLabelText('BRouter URL');
    expect(screen.queryByText(hint)).not.toBeInTheDocument();
    a.unmount();
    stubApi({ 'GET /api/settings': [200, settingsFixture({ brouter: { url: 'http://remote:17777', segments_dir: '/segments' } })] });
    const b = renderWithClient(<IntegrationsForm />);
    expect(await screen.findByText(hint)).toBeInTheDocument();
    b.unmount();
    stubApi({ 'GET /api/settings': [200, settingsFixture({ brouter: { url: 'http://remote:17777', segments_dir: '' } })] });
    renderWithClient(<IntegrationsForm />);
    await screen.findByLabelText('BRouter URL');
    expect(screen.queryByText(hint)).not.toBeInTheDocument();
  });
});
