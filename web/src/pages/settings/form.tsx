import { useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import { parseSettingError } from '@/lib/errors';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';

export type SettingsOut = components['schemas']['SettingsOut'];
export type SettingsPatch = components['schemas']['SettingsPatch'];
export type Value = string | number | boolean | string[];
type Flat = Record<string, Value>;

export const SETTINGS_KEY = ['settings'] as const;

export function useSettings() {
  return useQuery<SettingsOut>({
    queryKey: SETTINGS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/settings')),
  });
}

function useSaveSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (patch: SettingsPatch) => unwrap(await api.PATCH('/api/settings', { body: patch })),
    onSuccess: (data) => qc.setQueryData(SETTINGS_KEY, data),
  });
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

/** {"ha.url": x} becomes {ha: {url: x}}. */
function nest(changes: Flat): SettingsPatch {
  const out: Record<string, Record<string, Value>> = {};
  for (const [k, v] of Object.entries(changes)) {
    const [section, field] = k.split('.');
    (out[section] ??= {})[field] = v;
  }
  return out as SettingsPatch;
}

/**
 * Draft state for a settings form over a flat "section.field" view of the server
 * values. Only fields that differ are sent. Secret fields are absent from `server`;
 * a draft entry for one means "replace" ("" = clear) and no entry means "keep".
 */
export function useSettingsForm(server: Flat | undefined, onSaved?: () => void) {
  const [draft, setDraft] = useState<Record<string, Value>>({});
  const [err, setErr] = useState<{ field?: string; text: string } | null>(null);
  const [saved, setSaved] = useState(false);
  // Bumped after each save so uncontrolled SecretInputs remount empty.
  const [version, setVersion] = useState(0);
  const save = useSaveSettings();

  const changes = (): Flat => {
    const out: Flat = {};
    for (const [k, v] of Object.entries(draft)) {
      if (!same(v, server?.[k])) out[k] = v;
    }
    return out;
  };
  const dirty = Object.keys(changes()).length > 0;

  const edit = (k: string, v: Value | undefined) => {
    setSaved(false);
    setErr(null);
    setDraft((p) => {
      const n = { ...p };
      if (v === undefined) delete n[k];
      else n[k] = v;
      return n;
    });
  };

  async function submit() {
    const ch = changes();
    if (Object.keys(ch).length === 0) return;
    const bad = Object.entries(ch).find(([, v]) => typeof v === 'number' && Number.isNaN(v));
    if (bad) {
      setErr({ field: bad[0], text: 'Enter a number.' });
      return;
    }
    setErr(null);
    try {
      await save.mutateAsync(nest(ch));
      setDraft({});
      setVersion((v) => v + 1);
      setSaved(true);
      onSaved?.();
    } catch (e) {
      setErr(parseSettingError(e));
    }
  }

  return {
    value: <T extends Value | undefined>(k: string): T => (k in draft ? draft[k] : server?.[k]) as T,
    edit,
    submit,
    dirty,
    pending: save.isPending,
    saved,
    version,
    /** Error text for one field, shown under it. */
    fieldError: (k: string) => (err?.field === k ? err.text : undefined),
    /** Errors for fields not on screen (or with no field) go to the banner. */
    banner: (shown: string[]) =>
      err && !(err.field && shown.includes(err.field)) ? (err.field ? `${err.field}: ${err.text}` : err.text) : undefined,
    secret: (k: string) => (v: string | undefined) => edit(k, v),
  };
}

export function SaveBar({
  dirty,
  pending,
  saved,
  banner,
}: {
  dirty: boolean;
  pending: boolean;
  saved: boolean;
  banner?: string;
}) {
  return (
    <div className="flex flex-col gap-3">
      {banner && <Banner tone="error">{banner}</Banner>}
      <div className="flex items-center gap-3">
        <Button type="submit" variant="primary" disabled={!dirty || pending}>
          {pending ? 'Saving…' : 'Save'}
        </Button>
        {saved && (
          <span role="status" className="text-sm text-ok">
            Saved
          </span>
        )}
      </div>
    </div>
  );
}

/** Loads settings, then renders the form body; shows load errors. */
export function SettingsGate({ children }: { children: (s: SettingsOut) => ReactNode }) {
  const q = useSettings();
  if (q.isPending) {
    return (
      <p role="status" className="text-dim">
        Loading…
      </p>
    );
  }
  if (q.isError) return <Banner tone="error">Couldn't load settings.</Banner>;
  return <>{children(q.data)}</>;
}

export function FormCard({ title, children, compact }: { title: string; children: ReactNode; compact?: boolean }) {
  if (compact) return <div className="flex flex-col gap-4">{children}</div>;
  return (
    <Card className="flex flex-col gap-4">
      <h2 className="numeral text-xl">{title}</h2>
      {children}
    </Card>
  );
}
