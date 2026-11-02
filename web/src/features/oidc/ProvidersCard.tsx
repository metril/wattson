import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { ProviderForm } from './ProviderForm';
import { PROVIDERS_KEY, useProviders, type ProviderOut } from './queries';
import { RedirectUri } from './RedirectUri';

/** Admin list of OIDC providers with add, edit and delete. */
export function ProvidersCard() {
  const q = useProviders();
  const qc = useQueryClient();
  const [editing, setEditing] = useState<number | 'new' | null>(null);
  const [confirming, setConfirming] = useState<number | null>(null);
  const del = useMutation({
    mutationFn: async (id: number) =>
      unwrap(await api.DELETE('/api/admin/oidc-providers/{id}', { params: { path: { id } } })),
    onSuccess: () => {
      setConfirming(null);
      void qc.invalidateQueries({ queryKey: PROVIDERS_KEY });
    },
  });

  const row = (p: ProviderOut) => (
    <li key={p.id} className="flex flex-col gap-3 py-4">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{p.display_name}</span>
        <code className="text-xs text-dim">{p.slug}</code>
        {!p.enabled && <span className="rounded-control bg-raised px-2 py-0.5 text-xs text-warn">disabled</span>}
        <div className="ml-auto flex gap-2">
          <Button
            aria-label={`${editing === p.id ? 'Close' : 'Edit'} ${p.display_name}`}
            onClick={() => setEditing(editing === p.id ? null : p.id)}
          >
            {editing === p.id ? 'Close' : 'Edit'}
          </Button>
          {confirming === p.id ? (
            <>
              <Button
                variant="danger"
                aria-label={`Confirm delete ${p.display_name}`}
                disabled={del.isPending}
                onClick={() => del.mutate(p.id)}
              >
                Confirm delete
              </Button>
              <Button onClick={() => setConfirming(null)}>Keep</Button>
            </>
          ) : (
            <Button aria-label={`Delete ${p.display_name}`} onClick={() => setConfirming(p.id)}>
              Delete
            </Button>
          )}
        </div>
      </div>
      {editing === p.id ? (
        <ProviderForm initial={p} onDone={() => setEditing(null)} />
      ) : (
        <RedirectUri uri={p.redirect_uri} name={p.display_name} />
      )}
    </li>
  );

  return (
    <Card className="flex flex-col gap-4">
      <div>
        <h2 className="numeral text-xl">Single sign-on</h2>
        <p className="text-sm text-dim">
          OpenID Connect providers such as Authentik, Keycloak or Authelia. Register each redirect URI at its
          provider; it is built from the Public URL in General settings.
        </p>
      </div>
      {q.isPending && (
        <p role="status" className="text-dim">
          Loading…
        </p>
      )}
      {q.isError && <Banner tone="error">Couldn't load providers.</Banner>}
      {q.data && q.data.length === 0 && editing !== 'new' && <p className="text-dim">No providers yet.</p>}
      {q.data && q.data.length > 0 && <ul className="divide-y divide-line">{q.data.map(row)}</ul>}
      {del.isError && <Banner tone="error">{errMessage(del.error)}</Banner>}
      {editing === 'new' ? (
        <ProviderForm onDone={() => setEditing(null)} />
      ) : (
        <div>
          <Button onClick={() => setEditing('new')}>Add provider</Button>
        </div>
      )}
    </Card>
  );
}
