import { useEffect, useState } from 'react';
import { TextArea } from '@/components/TextArea';
import { Toggle } from '@/components/Toggle';
import { ProvidersCard } from '@/features/oidc/ProvidersCard';
import { FormCard, SaveBar, SettingsGate, useSettingsForm, type SettingsOut } from './form';

const parseList = (s: string) => s.split(/[\s,]+/).filter(Boolean);

function Body({ s }: { s: SettingsOut }) {
  const f = useSettingsForm({
    'auth.allow_local_login': s.auth.allow_local_login,
    'auth.trusted_proxies': s.auth.trusted_proxies,
  });
  const [proxies, setProxies] = useState(s.auth.trusted_proxies.join('\n'));
  // After a save the server's normalised list replaces whatever was typed.
  useEffect(() => setProxies(s.auth.trusted_proxies.join('\n')), [f.version]); // eslint-disable-line react-hooks/exhaustive-deps
  const keys = ['auth.allow_local_login', 'auth.trusted_proxies'];
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void f.submit();
      }}
    >
      <FormCard title="Sign-in">
        <Toggle
          label="Allow local login"
          checked={f.value<boolean>('auth.allow_local_login')}
          onChange={(v) => f.edit('auth.allow_local_login', v)}
          hint="The setup admin can always sign in locally."
        />
        <TextArea
          label="Trusted proxies"
          value={proxies}
          placeholder={'172.18.0.0/16\n10.0.0.5'}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => {
            setProxies(e.target.value);
            f.edit('auth.trusted_proxies', parseList(e.target.value));
          }}
          error={f.fieldError('auth.trusted_proxies')}
          hint="Reverse proxies in front of Wattson, one CIDR or IP per line (at most 64). Only requests from these addresses may set X-Forwarded-For and X-Forwarded-Proto, which Wattson uses for the client address in login rate limiting and to mark cookies Secure behind https. Leave empty when browsers connect directly; otherwise every login appears to come from the proxy."
        />
        <SaveBar dirty={f.dirty} pending={f.pending} saved={f.saved} banner={f.banner(keys)} />
      </FormCard>
    </form>
  );
}

/** Authentication settings: local login, trusted proxies and OIDC providers. */
export function AuthPage() {
  return (
    <div className="flex flex-col gap-4">
      <SettingsGate>{(s) => <Body s={s} />}</SettingsGate>
      <ProvidersCard />
    </div>
  );
}
