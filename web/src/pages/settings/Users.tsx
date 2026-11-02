import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, ApiError, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Card } from '@/components/Card';
import { Toggle } from '@/components/Toggle';

type User = components['schemas']['UserOut'];
const USERS_KEY = ['users'] as const;

function refusal(e: unknown): string {
  if (e instanceof ApiError && e.code === 'last_admin') return 'At least one admin must remain.';
  if (e instanceof ApiError && e.code === 'bootstrap_protected') {
    return "The setup admin can't be demoted or disabled.";
  }
  return errMessage(e);
}

/** Admin: every account with admin and disabled switches. */
export function UsersPage() {
  const qc = useQueryClient();
  const q = useQuery<User[]>({
    queryKey: USERS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/users')),
  });
  const [error, setError] = useState<string | null>(null);
  const patch = useMutation({
    mutationFn: async (v: { id: number; body: { is_admin?: boolean; disabled?: boolean } }) =>
      unwrap(await api.PATCH('/api/users/{id}', { params: { path: { id: v.id } }, body: v.body })),
    onMutate: () => setError(null),
    onSuccess: (u) =>
      qc.setQueryData<User[]>(USERS_KEY, (old) => old?.map((x) => (x.id === u.id ? u : x))),
    onError: (e) => {
      setError(refusal(e));
      void qc.invalidateQueries({ queryKey: USERS_KEY });
    },
  });

  return (
    <Card className="flex flex-col gap-4">
      <div>
        <h2 className="numeral text-xl">Users</h2>
        <p className="text-sm text-dim">
          Disabling a user signs them out everywhere. The setup admin can't be demoted or disabled.
        </p>
      </div>
      {error && <Banner tone="error">{error}</Banner>}
      {q.isPending && (
        <p role="status" className="text-dim">
          Loading…
        </p>
      )}
      {q.isError && <Banner tone="error">Couldn't load users.</Banner>}
      {q.data && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[40rem] text-left text-sm">
            <thead className="text-dim">
              <tr>
                <th className="px-2 py-2 font-medium">Username</th>
                <th className="px-2 py-2 font-medium">Name</th>
                <th className="px-2 py-2 font-medium">Email</th>
                <th className="px-2 py-2 font-medium">Source</th>
                <th className="px-2 py-2 font-medium">Admin</th>
                <th className="px-2 py-2 font-medium">Disabled</th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((u) => {
                const locked = u.is_bootstrap || (patch.isPending && patch.variables?.id === u.id);
                return (
                  <tr key={u.id} className="border-t border-line">
                    <td className="px-2 py-1 font-medium">
                      {u.username}
                      {u.is_bootstrap && <span className="ml-2 text-xs text-dim">setup admin</span>}
                    </td>
                    <td className="px-2 py-1">{u.display_name}</td>
                    <td className="px-2 py-1 break-all">{u.email || '—'}</td>
                    <td className="px-2 py-1">{u.oidc ? 'OIDC' : 'Local'}</td>
                    <td className="px-2 py-1">
                      <Toggle
                        srOnly
                        label={`Admin: ${u.username}`}
                        checked={u.is_admin}
                        disabled={locked}
                        onChange={(v) => patch.mutate({ id: u.id, body: { is_admin: v } })}
                      />
                    </td>
                    <td className="px-2 py-1">
                      <Toggle
                        srOnly
                        label={`Disabled: ${u.username}`}
                        checked={u.disabled}
                        disabled={locked}
                        onChange={(v) => patch.mutate({ id: u.id, body: { disabled: v } })}
                      />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}
