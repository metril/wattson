import { Link } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import { Field } from '@/components/Field';
import { SecretInput } from '@/components/SecretInput';
import { Select } from '@/components/Select';
import { TestButton } from '@/components/TestButton';
import { Toggle } from '@/components/Toggle';
import { FormCard, SaveBar, SettingsGate, useSettingsForm, type SettingsOut } from './form';

const BUNDLED_BROUTER = 'http://brouter:17777';
const MQTT_STATUS_KEY = ['mqtt-status'] as const;
const NO_CREDS = 'Put credentials in the fields below, not in the URL (no user:pass@).';

/** Host (no port) Strava wants as the Authorization Callback Domain. */
function callbackDomain(publicUrl: string): string {
  try {
    return new URL(publicUrl).hostname;
  } catch {
    return location.hostname;
  }
}

function MqttStatus() {
  const q = useQuery({
    queryKey: MQTT_STATUS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/mqtt/status')),
    refetchInterval: 5000,
    retry: false,
  });
  if (!q.data) return null;
  const { enabled, connected, error } = q.data;
  const [text, tone] = !enabled
    ? ['Disabled', 'text-dim']
    : connected
      ? ['Connected', 'text-ok']
      : [error ? `Not connected: ${error}` : 'Not connected', 'text-danger'];
  return (
    <p aria-live="polite" className={`text-sm ${tone}`}>
      {text}
    </p>
  );
}

function Body({ s }: { s: SettingsOut }) {
  const qc = useQueryClient();
  const f = useSettingsForm({
    'ha.url': s.ha.url,
    'mqtt.url': s.mqtt.url,
    'mqtt.username': s.mqtt.username,
    'mqtt.discovery_prefix': s.mqtt.discovery_prefix,
    'mqtt.tls_insecure_skip_verify': s.mqtt.tls_insecure_skip_verify,
    'brouter.url': s.brouter.url,
    'brouter.profile': s.brouter.profile,
    'brouter.segments_dir': s.brouter.segments_dir,
    'brouter.segments_url': s.brouter.segments_url,
    'brouter.public_fallback': s.brouter.public_fallback,
    'brouter.public_url': s.brouter.public_url,
    'elevation.opentopodata': s.elevation.opentopodata,
    'elevation.opentopodata_url': s.elevation.opentopodata_url,
    'strava.client_id': s.strava.client_id,
  }, () => void qc.invalidateQueries({ queryKey: MQTT_STATUS_KEY }));
  const text = (key: string, label: string, extra: { hint?: string; type?: string; placeholder?: string } = {}) => (
    <Field
      label={label}
      value={f.value<string>(key)}
      onChange={(e) => f.edit(key, e.target.value)}
      error={f.fieldError(key)}
      autoComplete="off"
      {...extra}
    />
  );
  const mqttUrl = f.value<string>('mqtt.url');
  const mqttUser = f.value<string>('mqtt.username');
  const mqttPass = f.value<string | undefined>('mqtt.password');
  const mqttTls = f.value<boolean>('mqtt.tls_insecure_skip_verify');
  const tlsShown = mqttUrl.trim().startsWith('mqtts://') || mqttTls;
  const fallback = f.value<boolean>('brouter.public_fallback');
  const topo = f.value<boolean>('elevation.opentopodata');
  // Only what is on screen is validated and sent.
  const shown = [
    'ha.url', 'ha.token', 'mqtt.url', 'mqtt.username', 'mqtt.password', 'mqtt.discovery_prefix',
    ...(tlsShown ? ['mqtt.tls_insecure_skip_verify'] : []),
    'brouter.url', 'brouter.profile', 'brouter.segments_dir', 'brouter.segments_url', 'brouter.public_fallback',
    ...(fallback ? ['brouter.public_url'] : []),
    'elevation.opentopodata',
    ...(topo ? ['elevation.opentopodata_url'] : []),
    'mapillary.token', 'strava.client_id', 'strava.client_secret',
  ];
  const brouterUrl = f.value<string>('brouter.url');
  const segmentsDir = f.value<string>('brouter.segments_dir');
  const remoteWithRegions = brouterUrl.trim() !== '' && brouterUrl.trim().replace(/\/+$/, '') !== BUNDLED_BROUTER && segmentsDir.trim() !== '';
  const haUrl = f.value<string>('ha.url');
  const haToken = f.value<string | undefined>('ha.token');
  const mapillaryToken = f.value<string | undefined>('mapillary.token');

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        void f.submit(shown);
      }}
    >
      <FormCard title="Home Assistant">
        {text('ha.url', 'Home Assistant URL', { type: 'url', placeholder: 'http://homeassistant.local:8123', hint: NO_CREDS })}
        <SecretInput
          key={`ha-${f.version}`}
          label="Long-lived access token"
          isSet={s.ha.token_set}
          onChange={f.secret('ha.token')}
          error={f.fieldError('ha.token')}
          hint="Create one on your Home Assistant profile page. Enter the token to test a different URL: the saved token is only used with the saved URL."
        />
        <TestButton
          label="Test Home Assistant"
          run={async () =>
            unwrap(
              await api.POST('/api/settings/test/ha', {
                body: { ...(haUrl ? { url: haUrl } : {}), ...(haToken ? { token: haToken } : {}) },
              }),
            )
          }
        />
      </FormCard>
      <FormCard title="MQTT">
        {text('mqtt.url', 'MQTT broker URL', { placeholder: 'mqtt://broker.local:1883', hint: `mqtt:// or mqtts://. ${NO_CREDS}` })}
        {text('mqtt.username', 'MQTT username')}
        <SecretInput
          key={`mqtt-${f.version}`}
          label="MQTT password"
          isSet={s.mqtt.password_set}
          onChange={f.secret('mqtt.password')}
          error={f.fieldError('mqtt.password')}
        />
        {text('mqtt.discovery_prefix', 'MQTT discovery prefix', {
          hint: 'Home Assistant MQTT discovery prefix. Leave as homeassistant unless you changed it in Home Assistant.',
        })}
        {tlsShown && (
          <Toggle
            label="Skip TLS certificate checks"
            checked={mqttTls}
            onChange={(v) => f.edit('mqtt.tls_insecure_skip_verify', v)}
            hint="For a self-signed broker on a trusted network only. Anyone on the network path could impersonate the broker and read the credentials."
          />
        )}
        <TestButton
          label="Test MQTT"
          run={async () =>
            unwrap(
              await api.POST('/api/settings/test/mqtt', {
                body: {
                  ...(mqttUrl ? { url: mqttUrl } : {}),
                  ...(mqttUser ? { username: mqttUser } : {}),
                  ...(mqttPass ? { password: mqttPass } : {}),
                  ...(tlsShown ? { tls_insecure_skip_verify: mqttTls } : {}),
                },
              }),
            )
          }
        />
        <MqttStatus />
      </FormCard>
      <FormCard title="BRouter">
        {text('brouter.url', 'BRouter URL', {
          type: 'url',
          placeholder: BUNDLED_BROUTER,
          hint: remoteWithRegions
            ? "Used to plan routes drawn on the map. Using a remote BRouter? Clear 'Segments folder' so drawing doesn't require local regions."
            : 'Used to plan routes drawn on the map.',
        })}
        <Select
          label="Default routing profile"
          value={f.value<string>('brouter.profile')}
          onChange={(e) => f.edit('brouter.profile', e.target.value)}
        >
          <option value="trekking">Trekking</option>
          <option value="fastbike">Fast bike</option>
          <option value="safety">Safety</option>
          <option value="shortest">Shortest</option>
        </Select>
        {text('brouter.segments_dir', 'Segments folder', {
          placeholder: '/segments',
          hint: 'Where map regions are stored. Leave empty when using a remote BRouter.',
        })}
        {text('brouter.segments_url', 'Segments source URL', {
          type: 'url',
          hint: 'Map regions are downloaded from here. Must end with /.',
        })}
        <Toggle
          label="Use public brouter.de when a region is missing"
          checked={fallback}
          onChange={(v) => f.edit('brouter.public_fallback', v)}
          hint="Sends your waypoints to brouter.de"
        />
        {fallback && text('brouter.public_url', 'Public BRouter URL', { type: 'url' })}
        <TestButton
          label="Test BRouter"
          run={async () =>
            unwrap(await api.POST('/api/settings/test/brouter', { body: brouterUrl ? { url: brouterUrl } : {} }))
          }
        />
        <Link to="/settings/maps" className="inline-flex min-h-touch items-center self-start text-accent underline">
          Manage map regions
        </Link>
      </FormCard>
      <FormCard title="Elevation">
        <Toggle
          label="Use OpenTopoData for missing elevation and above 60° latitude"
          checked={topo}
          onChange={(v) => f.edit('elevation.opentopodata', v)}
          hint="Sends route coordinates to the OpenTopoData server"
        />
        {topo &&
          text('elevation.opentopodata_url', 'OpenTopoData URL', {
            type: 'url',
            hint: 'API base URL including the dataset, such as https://api.opentopodata.org/v1/mapzen.',
          })}
      </FormCard>
      <FormCard title="Street imagery">
        <SecretInput
          key={`mapillary-${f.version}`}
          label="Mapillary access token"
          isSet={s.mapillary.token_set}
          onChange={f.secret('mapillary.token')}
          error={f.fieldError('mapillary.token')}
          hint="Street photos are fetched by the Wattson server; the token never reaches viewers' browsers."
        />
        <TestButton
          label="Test Mapillary"
          run={async () => unwrap(await api.POST('/api/settings/test/mapillary', { body: mapillaryToken ? { token: mapillaryToken } : {} }))}
        />
      </FormCard>
      <FormCard title="Strava">
        <p className="text-sm text-dim">
          Each new Strava API app can connect 1 athlete until Strava raises its capacity. Riders can add their own app
          on the Riders page. Manual FIT download always works.
        </p>
        <p className="text-sm text-dim">Authorization Callback Domain: {callbackDomain(s.general.public_url)}</p>
        {text('strava.client_id', 'Strava client ID')}
        <SecretInput
          key={`strava-${f.version}`}
          label="Strava client secret"
          isSet={s.strava.client_secret_set}
          onChange={f.secret('strava.client_secret')}
          error={f.fieldError('strava.client_secret')}
        />
      </FormCard>
      <SaveBar dirty={f.dirtyFor(shown)} pending={f.pending} saved={f.saved} banner={f.banner(shown)} />
    </form>
  );
}

export function IntegrationsForm() {
  return <SettingsGate>{(s) => <Body s={s} />}</SettingsGate>;
}
