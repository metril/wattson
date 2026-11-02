import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { StravaCard, stravaReasonText } from './StravaCard';
import { apiError, renderWithClient, stubApi, userFixture } from '@/test-utils';
import type { components } from '@/lib/openapi';

type Link = components['schemas']['StravaLinkOut'];
const link = (over: Partial<Link> = {}): Link => ({
  athlete_id: 0,
  athlete_name: '',
  auto_upload: false,
  available: true,
  callback_domain: 'w.example',
  client_id: '',
  client_secret_set: false,
  connected: false,
  connected_at: null,
  redirect_uri: 'https://w.example/api/strava/callback',
  scope: '',
  uses_own_app: false,
  ...over,
});
const connected = (over: Partial<Link> = {}) =>
  link({ connected: true, athlete_id: 5, athlete_name: 'Ada Lovelace', scope: 'read,activity:write', ...over });
const me = (via: 'session' | 'token' = 'session'): [number, unknown] => [
  200,
  { user: userFixture({ id: 1 }), scopes: ['read', 'control'], via },
];

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('StravaCard', () => {
  it('explains when no Strava app is configured', async () => {
    stubApi({ 'GET /api/auth/me': me(), 'GET /api/riders/1/strava': [200, link({ available: false })] });
    renderWithClient(<StravaCard riderId={1} />);
    expect(
      await screen.findByText(
        'Strava is not set up. An admin can add a shared Strava app in Settings → Integrations, or use your own app below.',
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Connect Strava' })).not.toBeInTheDocument();
  });

  it('links to the connect flow and returns to the Riders page', async () => {
    stubApi({ 'GET /api/auth/me': me(), 'GET /api/riders/1/strava': [200, link()] });
    renderWithClient(<StravaCard riderId={1} />);
    expect(await screen.findByRole('link', { name: 'Connect Strava' })).toHaveAttribute(
      'href',
      '/api/riders/1/strava/connect?return_to=%2Friders',
    );
  });

  it('asks a paired display to connect from a signed-in browser', async () => {
    stubApi({ 'GET /api/auth/me': me('token'), 'GET /api/riders/1/strava': [200, link()] });
    renderWithClient(<StravaCard riderId={1} />);
    expect(await screen.findByText('Connect Strava from a signed-in browser')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Connect Strava' })).not.toBeInTheDocument();
  });

  it('shows the athlete and toggles auto-upload', async () => {
    const api = stubApi({
      'GET /api/auth/me': me(),
      'GET /api/riders/1/strava': [200, connected()],
      'PATCH /api/riders/1/strava': [200, connected({ auto_upload: true })],
    });
    renderWithClient(<StravaCard riderId={1} />);
    expect(await screen.findByText('Connected as Ada Lovelace')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('switch', { name: 'Upload rides automatically' }));
    await waitFor(() => expect(api.bodies('PATCH /api/riders/1/strava')).toEqual([{ auto_upload: true }]));
  });

  it('PATCHes only the typed client ID for the own app and shows the callback domain', async () => {
    const api = stubApi({
      'GET /api/auth/me': me(),
      'GET /api/riders/1/strava': [200, link()],
      'PATCH /api/riders/1/strava': [200, link({ client_id: '999', uses_own_app: true })],
    });
    renderWithClient(<StravaCard riderId={1} />);
    await userEvent.click(await screen.findByRole('button', { name: /use my own strava app/i }));
    expect(screen.getByText('Authorization Callback Domain: w.example')).toBeInTheDocument();
    expect(screen.getByLabelText('Strava client secret')).toHaveValue('');
    await userEvent.type(screen.getByLabelText('Strava client ID'), '999');
    await userEvent.click(screen.getByRole('button', { name: 'Save Strava app' }));
    await waitFor(() => expect(api.bodies('PATCH /api/riders/1/strava')).toEqual([{ client_id: '999' }]));
  });

  it('surfaces a server error from saving the own app', async () => {
    stubApi({
      'GET /api/auth/me': me(),
      'GET /api/riders/1/strava': [200, link()],
      'PATCH /api/riders/1/strava': apiError(400, 'invalid_request', 'client_id: digits only'),
    });
    renderWithClient(<StravaCard riderId={1} />);
    await userEvent.click(await screen.findByRole('button', { name: /use my own strava app/i }));
    await userEvent.type(screen.getByLabelText('Strava client ID'), '9x');
    await userEvent.click(screen.getByRole('button', { name: 'Save Strava app' }));
    expect(await screen.findByText('client_id: digits only')).toBeInTheDocument();
  });

  it('needs two taps to disconnect', async () => {
    const api = stubApi({
      'GET /api/auth/me': me(),
      'GET /api/riders/1/strava': [200, connected()],
      'POST /api/riders/1/strava/disconnect': [204],
    });
    renderWithClient(<StravaCard riderId={1} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Disconnect' }));
    expect(api.bodies('POST /api/riders/1/strava/disconnect')).toHaveLength(0);
    await userEvent.click(screen.getByRole('button', { name: 'Tap again to disconnect' }));
    await waitFor(() => expect(api.bodies('POST /api/riders/1/strava/disconnect')).toHaveLength(1));
  });
});

describe('stravaReasonText', () => {
  it('maps every reason', () => {
    expect(stravaReasonText('denied')).toBe('Strava connection was cancelled.');
    expect(stravaReasonText('scope')).toBe(
      'Strava was connected without upload permission. Connect again and keep “Upload your activities” ticked.',
    );
    expect(stravaReasonText('state')).toBe('The Strava sign-in expired. Try again.');
    expect(stravaReasonText('exchange_failed')).toBe('Strava rejected the connection. Check the client ID and secret.');
    expect(stravaReasonText('not_configured')).toBe('Strava is not set up.');
    expect(stravaReasonText('connected')).toBe('Strava connected.');
    expect(stravaReasonText('zzz')).toBe('Strava connection failed.');
  });
});
