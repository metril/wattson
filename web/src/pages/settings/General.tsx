import { Field } from '@/components/Field';
import { Toggle } from '@/components/Toggle';
import { FormCard, SaveBar, SettingsGate, useSettingsForm, type SettingsOut } from './form';

function Body({ s }: { s: SettingsOut }) {
  const f = useSettingsForm({
    'general.public_url': s.general.public_url,
    'auth.session_ttl_hours': s.auth.session_ttl_hours,
    'auth.allow_local_login': s.auth.allow_local_login,
  });
  const ttl = f.value<number>('auth.session_ttl_hours');
  const keys = ['general.public_url', 'auth.session_ttl_hours', 'auth.allow_local_login'];
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void f.submit();
      }}
    >
      <FormCard title="General">
        <Field
          label="Public URL"
          type="url"
          inputMode="url"
          placeholder="https://wattson.example.com"
          value={f.value<string>('general.public_url')}
          onChange={(e) => f.edit('general.public_url', e.target.value)}
          error={f.fieldError('general.public_url')}
          hint="Where browsers reach Wattson, with no path. Single sign-in redirect URIs are built from it."
        />
        <Field
          label="Session lifetime (hours)"
          type="number"
          min={1}
          max={8760}
          inputMode="numeric"
          value={Number.isNaN(ttl) ? '' : ttl}
          onChange={(e) => f.edit('auth.session_ttl_hours', e.target.value === '' ? NaN : Number(e.target.value))}
          error={f.fieldError('auth.session_ttl_hours')}
          hint="1 to 8760. Applies to sessions created after the change."
        />
        <Toggle
          label="Allow local login"
          checked={f.value<boolean>('auth.allow_local_login')}
          onChange={(v) => f.edit('auth.allow_local_login', v)}
          hint="The setup admin can always sign in locally."
        />
        <SaveBar dirty={f.dirty} pending={f.pending} saved={f.saved} banner={f.banner(keys)} />
      </FormCard>
    </form>
  );
}

export function GeneralForm() {
  return <SettingsGate>{(s) => <Body s={s} />}</SettingsGate>;
}
