import { useEffect, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import { ME_KEY, useMe } from '@/lib/auth';
import { errMessage } from '@/lib/errors';
import { RIDERS_KEY } from '@/pages/Riders';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { Field } from '@/components/Field';
import { Select } from '@/components/Select';
import type { Units } from '@/lib/format';

export function AccountPage() {
  const qc = useQueryClient();
  const me = useMe();
  const current = me.data?.user.display_name ?? '';
  const [name, setName] = useState(current);
  const [saved, setSaved] = useState(false);

  useEffect(() => setName(current), [current]);

  const save = useMutation({
    mutationFn: async () =>
      unwrap(await api.PATCH('/api/auth/me', { body: { display_name: name.trim() } })),
    onSuccess: (m) => {
      qc.setQueryData(ME_KEY, m);
      void qc.invalidateQueries({ queryKey: RIDERS_KEY });
      setSaved(true);
    },
    onMutate: () => setSaved(false),
  });

  const setUnits = useMutation({
    mutationFn: async (units: Units) => unwrap(await api.PATCH('/api/auth/me', { body: { units } })),
    onSuccess: (m) => qc.setQueryData(ME_KEY, m),
  });

  return (
    <div className="mx-auto flex max-w-xl flex-col gap-4">
      <h1 className="numeral text-2xl">Account</h1>
      <Card>
        <form
          noValidate
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <Field
            label="Display name"
            hint="Shown across the app. Riders that still use your old name are renamed too."
            value={name}
            maxLength={64}
            onChange={(e) => setName(e.target.value)}
          />
          {save.isError && <Banner tone="error">{errMessage(save.error)}</Banner>}
          {saved && <Banner tone="ok">Saved.</Banner>}
          <div>
            <Button type="submit" variant="primary" disabled={save.isPending || name.trim() === ''}>
              Save
            </Button>
          </div>
        </form>
      </Card>
      <Card className="flex flex-col gap-4">
        <Select
          label="Units"
          hint="Distance, speed and elevation across the app."
          value={me.data?.user.units ?? 'metric'}
          disabled={setUnits.isPending}
          onChange={(e) => setUnits.mutate(e.target.value as Units)}
        >
          <option value="metric">Metric (km, km/h, m)</option>
          <option value="imperial">Imperial (mi, mph, ft)</option>
        </Select>
        {setUnits.isError && <Banner tone="error">{errMessage(setUnits.error)}</Banner>}
      </Card>
    </div>
  );
}
