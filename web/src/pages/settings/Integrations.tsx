import { api, unwrap } from '@/lib/api';
import { Field } from '@/components/Field';
import { SecretInput } from '@/components/SecretInput';
import { TestButton } from '@/components/TestButton';
import { FormCard, SaveBar, SettingsGate, useSettingsForm, type SettingsOut } from './form';

const NO_CREDS = 'Put credentials in the fields below, not in the URL (no user:pass@).';

function Body({ s }: { s: SettingsOut }) {
  const f = useSettingsForm({
    'ha.url': s.ha.url,
    'mqtt.url': s.mqtt.url,
    'mqtt.username': s.mqtt.username,
    'brouter.url': s.brouter.url,
    'strava.client_id': s.strava.client_id,
  });
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
  const keys = ['ha.url', 'ha.token', 'mqtt.url', 'mqtt.username', 'mqtt.password', 'brouter.url', 'mapillary.token', 'strava.client_id', 'strava.client_secret'];
  const haUrl = f.value<string>('ha.url');
  const haToken = f.value<string | undefined>('ha.token');

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        void f.submit();
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
      </FormCard>
      <FormCard title="Routing and imagery">
        {text('brouter.url', 'BRouter URL', { type: 'url', placeholder: 'http://brouter:17777', hint: 'Used to plan routes.' })}
        <SecretInput
          key={`mapillary-${f.version}`}
          label="Mapillary access token"
          isSet={s.mapillary.token_set}
          onChange={f.secret('mapillary.token')}
          error={f.fieldError('mapillary.token')}
          hint="Street-level imagery along a route."
        />
      </FormCard>
      <FormCard title="Strava">
        {text('strava.client_id', 'Strava client ID')}
        <SecretInput
          key={`strava-${f.version}`}
          label="Strava client secret"
          isSet={s.strava.client_secret_set}
          onChange={f.secret('strava.client_secret')}
          error={f.fieldError('strava.client_secret')}
        />
      </FormCard>
      <SaveBar dirty={f.dirty} pending={f.pending} saved={f.saved} banner={f.banner(keys)} />
    </form>
  );
}

export function IntegrationsForm() {
  return <SettingsGate>{(s) => <Body s={s} />}</SettingsGate>;
}
