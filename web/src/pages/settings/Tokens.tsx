import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Copy, MonitorSmartphone } from 'lucide-react';
import { api, unwrap } from '@/lib/api';
import { useMe } from '@/lib/auth';
import { copyText } from '@/lib/clipboard';
import { errMessage } from '@/lib/errors';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { Field } from '@/components/Field';

type Token = components['schemas']['TokenOut'];
type Created = components['schemas']['CreatedToken'];

const TOKENS_KEY = ['tokens'] as const;
const SCOPES = [
  ['read', 'Read: view rides and settings'],
  ['control', 'Control: operate the bike'],
  ['admin', 'Admin: manage the server'],
] as const;

const pad = (n: number) => String(n).padStart(2, '0');
function kioskName(d = new Date()) {
  return `Kiosk ${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
const when = (s: string | null) => (s ? new Date(s).toLocaleString() : 'never');

type Shown = { kind: 'token'; token: Created } | { kind: 'pair'; token: Created; url: string; qr: string };

function CopyButton({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <Button
      onClick={async () => {
        setDone(await copyText(text));
      }}
    >
      <Copy aria-hidden className="size-5" />
      {done ? 'Copied' : 'Copy'}
    </Button>
  );
}

function Row({ t, onError }: { t: Token; onError: (m: string | null) => void }) {
  const qc = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const del = useMutation({
    mutationFn: async () => unwrap(await api.DELETE('/api/tokens/{id}', { params: { path: { id: t.id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: TOKENS_KEY }),
    onError: (e) => {
      setConfirm(false);
      onError(errMessage(e));
    },
  });
  return (
    <li className="flex flex-wrap items-center gap-3 border-t border-line py-3 first:border-t-0">
      <div className="min-w-0 flex-1 basis-56">
        <p className="truncate font-medium">{t.name}</p>
        <p className="font-mono text-sm text-dim">{t.prefix}…</p>
        <p className="text-xs text-dim">
          {t.scopes.join(', ')} · created {when(t.created_at)} · last used {when(t.last_used_at)}
        </p>
      </div>
      <Button
        variant={confirm ? 'danger' : 'secondary'}
        disabled={del.isPending}
        aria-label={confirm ? undefined : `Revoke ${t.name}`}
        onClick={() => {
          onError(null);
          if (confirm) del.mutate();
          else setConfirm(true);
        }}
      >
        {confirm ? 'Click again to confirm' : 'Revoke'}
      </Button>
    </li>
  );
}

export function TokensPage() {
  const qc = useQueryClient();
  const me = useMe();
  const isAdmin = !!me.data?.user.is_admin;
  const list = useQuery<Token[]>({
    queryKey: TOKENS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/tokens')),
  });
  // Admins know the configured public URL; everyone else uses this browser's origin.
  const pub = useQuery({
    queryKey: ['settings', 'public_url'],
    enabled: isAdmin,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/settings')).general.public_url,
  });

  const [name, setName] = useState('');
  const [scopes, setScopes] = useState<string[]>(['read']);
  const [shown, setShown] = useState<Shown | null>(null);
  const [error, setError] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: async () => unwrap(await api.POST('/api/tokens', { body: { name: name.trim(), scopes } })),
    onSuccess: (token) => {
      setShown({ kind: 'token', token });
      setName('');
      setScopes(['read']);
      void qc.invalidateQueries({ queryKey: TOKENS_KEY });
    },
    onError: (e) => setError(errMessage(e)),
  });

  const pair = useMutation({
    mutationFn: async (): Promise<Shown> => {
      const token = unwrap(
        await api.POST('/api/tokens', { body: { name: kioskName(), scopes: ['read', 'control'] } }),
      );
      const base = (pub.data || window.location.origin).replace(/\/+$/, '');
      const url = `${base}/kiosk#token=${token.token}`;
      // Lazy: the QR encoder stays out of the main bundle.
      const QR = await import('qrcode');
      const qr = await QR.toDataURL(url, { margin: 2, width: 320, color: { dark: '#000000', light: '#ffffff' } });
      void qc.invalidateQueries({ queryKey: TOKENS_KEY });
      return { kind: 'pair', token, url, qr };
    },
    onSuccess: setShown,
    onError: (e) => setError(errMessage(e)),
  });

  const toggle = (s: string, on: boolean) => setScopes((p) => (on ? [...p, s] : p.filter((x) => x !== s)));
  const visibleScopes = SCOPES.filter(([s]) => s !== 'admin' || isAdmin);

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4">
      <h1 className="numeral text-2xl">API tokens</h1>
      {error && <Banner tone="error">{error}</Banner>}

      {shown && (
        <Card className="flex flex-col gap-4" aria-label="New token">
          <Banner tone="warn">Shown once. Copy it now; it cannot be displayed again.</Banner>
          {shown.kind === 'pair' ? (
            <>
              <p className="text-dim">Scan this with the display to pair it. The link works on that display only.</p>
              <img src={shown.qr} alt="Pairing QR code" className="size-80 max-w-full self-center rounded-control bg-white" />
              <p className="font-mono text-sm break-all">{shown.url}</p>
              <div className="flex flex-wrap gap-3">
                <CopyButton text={shown.url} />
                <Button variant="primary" onClick={() => setShown(null)}>
                  Done
                </Button>
              </div>
            </>
          ) : (
            <>
              <p className="font-mono break-all">{shown.token.token}</p>
              <div className="flex flex-wrap gap-3">
                <CopyButton text={shown.token.token} />
                <Button variant="primary" onClick={() => setShown(null)}>
                  Done
                </Button>
              </div>
            </>
          )}
        </Card>
      )}

      <Card className="flex flex-col gap-4">
        <h2 className="numeral text-xl">Pair a display</h2>
        <p className="text-sm text-dim">
          Creates a token with read and control access and shows a QR code that signs a wall display or TV in.
        </p>
        <div>
          <Button onClick={() => { setError(null); pair.mutate(); }} disabled={pair.isPending}>
            <MonitorSmartphone aria-hidden className="size-5" />
            Pair a display
          </Button>
        </div>
      </Card>

      <Card>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            setError(null);
            create.mutate();
          }}
        >
          <h2 className="numeral text-xl">New token</h2>
          <Field label="Name" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />
          <fieldset className="flex flex-col">
            <legend className="text-sm font-medium text-dim">Scopes</legend>
            {visibleScopes.map(([s, label]) => (
              <label key={s} className="flex min-h-touch cursor-pointer items-center gap-3">
                <input
                  type="checkbox"
                  checked={scopes.includes(s)}
                  onChange={(e) => toggle(s, e.target.checked)}
                  className="size-6 accent-accent"
                />
                {label}
              </label>
            ))}
          </fieldset>
          <div>
            <Button type="submit" variant="primary" disabled={!name.trim() || scopes.length === 0 || create.isPending}>
              Create token
            </Button>
          </div>
        </form>
      </Card>

      <Card>
        <h2 className="numeral mb-2 text-xl">Your tokens</h2>
        {list.isPending ? (
          <p role="status" className="text-dim">
            Loading…
          </p>
        ) : list.isError ? (
          <Banner tone="error">Couldn't load tokens.</Banner>
        ) : list.data.length === 0 ? (
          <p className="text-dim">No tokens yet.</p>
        ) : (
          <ul>
            {list.data.map((t) => (
              <Row key={t.id} t={t} onError={setError} />
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}
