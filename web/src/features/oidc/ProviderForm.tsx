import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api, ApiError, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Field } from '@/components/Field';
import { SecretInput } from '@/components/SecretInput';
import { TestButton } from '@/components/TestButton';
import { Toggle } from '@/components/Toggle';
import { PROVIDERS_KEY, type ProviderOut } from './queries';
import { useSettings } from '@/pages/settings/form';
import { RedirectUri } from './RedirectUri';

type Create = components['schemas']['CreateProviderInBody'];

const slugify = (s: string) =>
  s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');

const toList = (s: string) =>
  s
    .split(/[,\n]/)
    .map((x) => x.trim())
    .filter(Boolean);

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

/** Create or edit one OIDC provider. `compact` drops the surrounding border (wizard). */
export function ProviderForm({
  initial,
  onDone,
  compact,
}: {
  initial?: ProviderOut;
  onDone: () => void;
  compact?: boolean;
}) {
  const qc = useQueryClient();
  const [name, setName] = useState(initial?.display_name ?? '');
  const [slug, setSlug] = useState(initial?.slug ?? '');
  const [slugEdited, setSlugEdited] = useState(false);
  const publicUrl = useSettings().data?.general.public_url;
  const previewUri = `${(publicUrl || window.location.origin).replace(/\/+$/, '')}/api/auth/oidc/${slug.trim() || '{slug}'}/callback`;
  const [discovery, setDiscovery] = useState(initial?.discovery_url ?? '');
  const [clientId, setClientId] = useState(initial?.client_id ?? '');
  const [secret, setSecret] = useState<string | undefined>();
  const [scopes, setScopes] = useState(initial?.scopes ?? 'openid profile email groups');
  const [groupsClaim, setGroupsClaim] = useState(initial?.groups_claim ?? 'groups');
  const [allowedGroups, setAllowedGroups] = useState((initial?.allowed_groups ?? []).join(', '));
  const [allowedEmails, setAllowedEmails] = useState((initial?.allowed_emails ?? []).join(', '));
  const [adminGroup, setAdminGroup] = useState(initial?.admin_group ?? '');
  const [autoCreate, setAutoCreate] = useState(initial?.auto_create_users ?? false);
  const [linkByEmail, setLinkByEmail] = useState(initial?.link_by_email ?? false);
  const [enabled, setEnabled] = useState(initial?.enabled ?? true);
  const [localError, setLocalError] = useState<string | null>(null);
  const [slugError, setSlugError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: async (): Promise<void> => {
      const full = {
        display_name: name.trim(),
        slug: slug.trim(),
        discovery_url: discovery.trim(),
        client_id: clientId.trim(),
        scopes: scopes.trim(),
        groups_claim: groupsClaim.trim(),
        allowed_groups: toList(allowedGroups),
        allowed_emails: toList(allowedEmails),
        admin_group: adminGroup.trim(),
        auto_create_users: autoCreate,
        link_by_email: linkByEmail,
        enabled,
      };
      if (!initial) {
        unwrap(await api.POST('/api/admin/oidc-providers', { body: { ...full, client_secret: secret ?? '' } as Create }));
        return;
      }
      // Edit sends only what changed; a blank secret is left out so the stored one is kept.
      const { slug: _immutable, ...fields } = full;
      const body: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(fields)) {
        if (!same(v, initial[k as keyof ProviderOut])) body[k] = v;
      }
      if (secret) body.client_secret = secret;
      if (Object.keys(body).length === 0) return;
      unwrap(await api.PATCH('/api/admin/oidc-providers/{id}', { params: { path: { id: initial.id } }, body }));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: PROVIDERS_KEY });
      onDone();
    },
    onError: (e) => {
      if (e instanceof ApiError && e.code === 'slug_taken') setSlugError('Slug already in use');
    },
  });

  function submit() {
    setLocalError(null);
    setSlugError(null);
    if (!initial && !secret) {
      setLocalError('Client secret is required.');
      return;
    }
    save.mutate();
  }

  const apiMessage =
    save.error && !(save.error instanceof ApiError && save.error.code === 'slug_taken') ? errMessage(save.error) : null;

  return (
    <form
      className={`flex flex-col gap-4 ${compact ? '' : 'rounded-panel border border-line bg-panel p-4'}`}
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      {initial ? (
        <RedirectUri uri={initial.redirect_uri} name={initial.display_name} />
      ) : (
        <div className="flex flex-col gap-1">
          <RedirectUri uri={previewUri} name="new provider" label="Redirect URI (preview)" />
          <p className="text-xs text-dim">
            Register this at your provider. It follows the slug and the Public URL setting
            {publicUrl ? '' : ' (not set, so this browser address is used)'}; the saved provider shows the final value.
          </p>
        </div>
      )}
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          label="Display name"
          value={name}
          hint="Text on the sign-in button."
          onChange={(e) => {
            setName(e.target.value);
            if (!initial && !slugEdited) setSlug(slugify(e.target.value));
          }}
        />
        <Field
          label="Slug"
          value={slug}
          disabled={!!initial}
          autoCapitalize="none"
          hint={initial ? "Can't be changed: it is part of the redirect URI." : 'Part of the login and redirect URLs. Lowercase letters, digits and dashes.'}
          error={slugError ?? undefined}
          onChange={(e) => {
            setSlugEdited(true);
            setSlug(e.target.value);
            setSlugError(null);
          }}
        />
      </div>
      <Field
        label="Discovery URL"
        type="url"
        inputMode="url"
        value={discovery}
        placeholder="https://auth.example.com/application/o/wattson/"
        hint="The issuer URL or the full .well-known/openid-configuration URL."
        onChange={(e) => setDiscovery(e.target.value)}
      />
      <TestButton
        label="Check discovery"
        disabled={!discovery.trim()}
        run={async () =>
          unwrap(await api.POST('/api/admin/oidc-providers/discover', { body: { discovery_url: discovery.trim() } }))
        }
      />
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Client ID" autoComplete="off" value={clientId} onChange={(e) => setClientId(e.target.value)} />
        <SecretInput
          label="Client secret"
          isSet={!!initial?.client_secret_set}
          clearable={false}
          onChange={setSecret}
          hint={initial ? 'Leave blank to keep the saved secret.' : 'Required.'}
        />
      </div>
      <Field
        label="Scopes"
        value={scopes}
        hint="Space-separated. openid, profile and email are always requested."
        onChange={(e) => setScopes(e.target.value)}
      />
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          label="Groups claim"
          value={groupsClaim}
          hint="Claim that lists the user's groups. Empty turns group handling off."
          onChange={(e) => setGroupsClaim(e.target.value)}
        />
        <Field
          label="Admin group"
          value={adminGroup}
          hint="Members become admins; others lose admin at their next sign-in. The setup admin and the last admin are never demoted."
          onChange={(e) => setAdminGroup(e.target.value)}
        />
        <Field
          label="Allowed groups"
          value={allowedGroups}
          placeholder="riders, admins"
          hint="Comma-separated. Empty allows everyone the provider authenticates."
          onChange={(e) => setAllowedGroups(e.target.value)}
        />
        <Field
          label="Allowed emails"
          value={allowedEmails}
          placeholder="me@example.com"
          hint="Comma-separated, verified addresses only. Empty allows any."
          onChange={(e) => setAllowedEmails(e.target.value)}
        />
      </div>
      <div className="flex flex-col">
        <Toggle
          label="Auto-create users"
          checked={autoCreate}
          onChange={setAutoCreate}
          hint="Create a local user on first sign-in. Off means only existing users can sign in."
        />
        <Toggle
          label="Link by email"
          checked={linkByEmail}
          onChange={setLinkByEmail}
          hint="Link a first sign-in to the one local user with the same verified email. Admin accounts, the setup admin included, are never linked this way; that sign-in is refused."
        />
        <Toggle label="Enabled" checked={enabled} onChange={setEnabled} />
      </div>
      {(localError || apiMessage) && <Banner tone="error">{localError ?? apiMessage}</Banner>}
      <div className="flex flex-wrap gap-3">
        <Button type="submit" variant="primary" disabled={save.isPending}>
          {initial ? 'Save provider' : 'Create provider'}
        </Button>
        <Button onClick={onDone}>Cancel</Button>
      </div>
    </form>
  );
}
