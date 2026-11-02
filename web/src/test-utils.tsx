import type { ReactElement } from 'react';
import { render } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import type { components } from '@/lib/openapi';

type Reply = [status: number, body?: unknown];
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Handler = Reply | ((body: any) => Reply | Promise<Reply>);

export interface Call {
  key: string;
  body: unknown;
}

const json = (status: number, body: unknown) =>
  status === 204
    ? new Response(null, { status })
    : new Response(JSON.stringify(body ?? {}), {
        status,
        headers: { 'content-type': 'application/json' },
      });

/** Replace fetch with a router keyed "METHOD /path"; returns the recorded calls. */
export function stubApi(routes: Record<string, Handler>) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const key = `${req.method} ${new URL(req.url).pathname}`;
      const text = await req.clone().text();
      let body: unknown;
      try {
        body = text ? JSON.parse(text) : undefined;
      } catch {
        body = text; // multipart uploads are not JSON
      }
      calls.push({ key, body });
      const h = routes[key];
      if (!h) return json(404, { error: { code: 'not_found', message: `no stub for ${key}` } });
      const [status, out] = typeof h === 'function' ? await h(body) : h;
      return json(status, out);
    }),
  );
  return {
    calls,
    bodies: (key: string) => calls.filter((c) => c.key === key).map((c) => c.body),
  };
}

export const apiError = (status: number, code: string, message = code, details?: unknown): Reply => [
  status,
  { error: { code, message, ...(details ? { details } : {}) } },
];

export function renderWithClient(ui: ReactElement, route = '/') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[route]}>{ui}</MemoryRouter>
    </QueryClientProvider>,
  );
}

export function settingsFixture(
  over: Partial<{ [K in keyof components['schemas']['SettingsOut']]: Partial<components['schemas']['SettingsOut'][K]> }> = {},
): components['schemas']['SettingsOut'] {
  const base: components['schemas']['SettingsOut'] = {
    auth: { allow_local_login: true, session_ttl_hours: 720, trusted_proxies: [] },
    bike: {
      address: '',
      driver: 'sim',
      esphome_host: '',
      esphome_key_set: false,
      ha_cadence_entity: '',
      ha_power_entity: '',
      ha_resistance_entity: '',
      idle_release_min: 10,
      sim_cadence_rpm: 80,
      transport: 'local',
    },
    brouter: {
      url: '',
      profile: 'trekking',
      segments_dir: '/segments',
      segments_url: 'https://brouter.de/brouter/segments4/',
      public_url: 'https://brouter.de',
      public_fallback: false,
    },
    elevation: { opentopodata: false, opentopodata_url: 'https://api.opentopodata.org/v1/mapzen' },
    general: { public_url: 'https://w.example' },
    ha: { token_set: true, url: 'http://ha.local' },
    heart_rate: { source: 'off', address: '', name: '', ha_entity: '' },
    mapillary: { token_set: false },
    mqtt: { discovery_prefix: 'homeassistant', password_set: false, url: '', username: '' },
    strava: { client_id: '', client_secret_set: false },
  };
  for (const k of Object.keys(over) as (keyof typeof base)[]) {
    Object.assign(base[k], over[k]);
  }
  return base;
}

export function providerFixture(
  over: Partial<components['schemas']['ProviderOut']> = {},
): components['schemas']['ProviderOut'] {
  return {
    id: 1,
    slug: 'authentik',
    display_name: 'Authentik',
    discovery_url: 'https://auth.example/application/o/wattson/',
    client_id: 'wattson',
    client_secret_set: true,
    scopes: 'openid profile email groups',
    groups_claim: 'groups',
    allowed_groups: [],
    allowed_emails: [],
    admin_group: '',
    auto_create_users: false,
    link_by_email: false,
    enabled: true,
    redirect_uri: 'https://w.example/api/auth/oidc/authentik/callback',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...over,
  };
}

export function userFixture(
  over: Partial<components['schemas']['UserOut']> = {},
): components['schemas']['UserOut'] {
  return {
    id: 2,
    username: 'bob',
    display_name: 'Bob',
    email: 'bob@example.com',
    is_admin: false,
    is_bootstrap: false,
    disabled: false,
    oidc: false,
    created_at: '2026-01-01T00:00:00Z',
    ...over,
  };
}

export function routeFixture(
  over: Partial<components['schemas']['RouteDetail']> = {},
): components['schemas']['RouteDetail'] {
  return {
    id: 1,
    name: 'Ramp',
    source: 'gpx',
    owner_id: 1,
    distance_m: 1000,
    ascent_m: 50,
    descent_m: 0,
    max_grade_pct: 6,
    min_grade_pct: 0,
    bbox: [7, 45, 7.1, 45.1],
    geojson: { type: 'LineString', coordinates: [[7, 45], [7.1, 45.1]] },
    spec: {},
    created_at: '2026-01-01T00:00:00Z',
    ...over,
  };
}

export function profileFixture(
  over: Partial<components['schemas']['ProfileOut']> = {},
): components['schemas']['ProfileOut'] {
  return {
    route_id: 1,
    step_m: 10,
    length_m: 20,
    lon: [7, 7.05, 7.1],
    lat: [45, 45.05, 45.1],
    ele_m: [100, 101, 102],
    grade_pct: [6, 6, 6],
    ...over,
  };
}
