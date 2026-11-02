import { useState } from 'react';
import { ChevronDown } from 'lucide-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import { useMe } from '@/lib/auth';
import { errMessage } from '@/lib/errors';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Field } from '@/components/Field';
import { SecretInput } from '@/components/SecretInput';
import { Toggle } from '@/components/Toggle';

type Link = components['schemas']['StravaLinkOut'];

export const stravaKey = (riderId: number) => ['riders', riderId, 'strava'] as const;

const REASONS: Record<string, string> = {
  denied: 'Strava connection was cancelled.',
  scope: 'Strava was connected without upload permission. Connect again and keep “Upload your activities” ticked.',
  state: 'The Strava sign-in expired. Try again.',
  exchange_failed: 'Strava rejected the connection. Check the client ID and secret.',
  not_configured: 'Strava is not set up.',
  connected: 'Strava connected.',
};

/** User-facing text for the `?strava=` result the callback appends to the return path. */
export function stravaReasonText(reason: string): string {
  return REASONS[reason] ?? 'Strava connection failed.';
}

const linkBtn =
  'inline-flex min-h-touch min-w-touch items-center justify-center gap-2 rounded-control bg-accent px-4 font-medium text-accent-ink hover:brightness-110';

function OwnApp({ riderId, link }: { riderId: number; link: Link }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(link.uses_own_app);
  const [clientId, setClientId] = useState(link.client_id);
  const [secret, setSecret] = useState<string | undefined>();
  const [version, setVersion] = useState(0);
  const [error, setError] = useState<string | null>(null);

  const changed: { client_id?: string; client_secret?: string } = {};
  if (clientId.trim() !== link.client_id) changed.client_id = clientId.trim();
  if (secret !== undefined) changed.client_secret = secret;
  const dirty = Object.keys(changed).length > 0;

  const save = useMutation({
    mutationFn: async () =>
      unwrap(await api.PATCH('/api/riders/{id}/strava', { params: { path: { id: riderId } }, body: changed })),
    onSuccess: (l) => {
      qc.setQueryData(stravaKey(riderId), l);
      setClientId(l.client_id);
      setSecret(undefined);
      setVersion((v) => v + 1);
    },
    onError: (e) => setError(errMessage(e)),
  });

  return (
    <div className="flex flex-col gap-3">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex min-h-touch items-center gap-2 text-left font-medium text-accent"
      >
        <ChevronDown aria-hidden className={`size-5 transition-transform ${open ? 'rotate-180' : ''}`} />
        Use my own Strava app
      </button>
      {open && (
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            setError(null);
            save.mutate();
          }}
        >
          <p className="text-sm text-dim">
            Create an app at strava.com/settings/api and enter its credentials. Changing them disconnects this rider.
          </p>
          <Field
            label="Strava client ID"
            inputMode="numeric"
            autoComplete="off"
            value={clientId}
            onChange={(e) => setClientId(e.target.value)}
          />
          <SecretInput
            key={`secret-${version}`}
            label="Strava client secret"
            isSet={link.client_secret_set}
            onChange={setSecret}
          />
          <p className="text-sm text-dim">Authorization Callback Domain: {link.callback_domain}</p>
          {error && <Banner tone="error">{error}</Banner>}
          <div>
            <Button type="submit" variant="primary" disabled={!dirty || save.isPending}>
              Save Strava app
            </Button>
          </div>
        </form>
      )}
    </div>
  );
}

/** One rider's Strava connection: connect, auto-upload, disconnect and an optional own app. */
export function StravaCard({ riderId }: { riderId: number }) {
  const qc = useQueryClient();
  const me = useMe();
  const kiosk = me.data?.via === 'token';
  const q = useQuery<Link>({
    queryKey: stravaKey(riderId),
    queryFn: async () => unwrap(await api.GET('/api/riders/{id}/strava', { params: { path: { id: riderId } } })),
  });
  const [confirm, setConfirm] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const auto = useMutation({
    mutationFn: async (v: boolean) =>
      unwrap(await api.PATCH('/api/riders/{id}/strava', { params: { path: { id: riderId } }, body: { auto_upload: v } })),
    onSuccess: (l) => qc.setQueryData(stravaKey(riderId), l),
    onError: (e) => setError(errMessage(e)),
  });
  const disconnect = useMutation({
    mutationFn: async () =>
      unwrap(await api.POST('/api/riders/{id}/strava/disconnect', { params: { path: { id: riderId } } })),
    onSuccess: () => {
      setConfirm(false);
      void qc.invalidateQueries({ queryKey: stravaKey(riderId) });
    },
    onError: (e) => {
      setConfirm(false);
      setError(errMessage(e));
    },
  });

  if (q.isPending) return <p className="text-sm text-dim">Loading Strava…</p>;
  if (q.isError) return <p className="text-sm text-dim">Couldn't load Strava status.</p>;
  const l = q.data;

  return (
    <section aria-label="Strava" className="flex flex-col gap-3 border-t border-line pt-4">
      <h3 className="numeral text-lg">Strava</h3>
      {l.connected ? (
        <>
          <p>Connected as {l.athlete_name}</p>
          <Toggle
            label="Upload rides automatically"
            checked={l.auto_upload}
            disabled={auto.isPending}
            onChange={(v) => {
              setError(null);
              auto.mutate(v);
            }}
          />
          <div>
            <Button
              variant={confirm ? 'danger' : 'secondary'}
              disabled={disconnect.isPending}
              onClick={() => {
                setError(null);
                if (confirm) disconnect.mutate();
                else setConfirm(true);
              }}
            >
              {confirm ? 'Tap again to disconnect' : 'Disconnect'}
            </Button>
          </div>
        </>
      ) : (
        <>
          {!l.available && (
            <p className="text-dim">
              Strava is not set up. An admin can add a shared Strava app in Settings → Integrations, or use your own app
              below.
            </p>
          )}
          {l.available &&
            (kiosk ? (
              <p className="text-dim">Connect Strava from a signed-in browser</p>
            ) : (
              <div>
                <a href={`/api/riders/${riderId}/strava/connect?return_to=${encodeURIComponent('/riders')}`} className={linkBtn}>
                  Connect Strava
                </a>
              </div>
            ))}
        </>
      )}
      {error && <Banner tone="error">{error}</Banner>}
      {!kiosk && <OwnApp key={`${l.client_id}-${l.uses_own_app}`} riderId={riderId} link={l} />}
    </section>
  );
}
