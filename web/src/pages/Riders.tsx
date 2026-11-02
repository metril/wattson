import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, Plus } from 'lucide-react';
import { api, ApiError, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { clearSelectedRiderId, getSelectedRiderId, setSelectedRiderId } from '@/lib/rider';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { Field } from '@/components/Field';

type Rider = components['schemas']['RiderOut'];
type Num = Exclude<keyof components['schemas']['PatchRiderInBody'], 'name'>;

export const RIDERS_KEY = ['riders'] as const;

interface Spec {
  key: Num;
  label: string;
  hint: string;
  def: number;
  min: number;
  max: number;
  step: string;
  int?: boolean;
}

// Defaults and ranges mirror the API documentation.
const SPECS: Spec[] = [
  { key: 'weight_kg', label: 'Rider weight (kg)', hint: '30 to 200.', def: 75, min: 30, max: 200, step: '0.1' },
  { key: 'bike_kg', label: 'Bike mass (kg)', hint: '0 to 30.', def: 8, min: 0, max: 30, step: '0.1' },
  { key: 'ftp_w', label: 'FTP (W)', hint: 'Functional threshold power, 50 to 600.', def: 200, min: 50, max: 600, step: '1', int: true },
  { key: 'cda', label: 'Drag area CdA (m²)', hint: '0.15 to 0.8.', def: 0.32, min: 0.15, max: 0.8, step: '0.01' },
  { key: 'crr', label: 'Rolling resistance Crr', hint: '0.001 to 0.02.', def: 0.004, min: 0.001, max: 0.02, step: '0.001' },
  { key: 'power_multiplier', label: 'Power multiplier (×)', hint: 'Scales measured power, 0.5 to 2.', def: 1, min: 0.5, max: 2, step: '0.01' },
  { key: 'min_level', label: 'Lowest resistance level', hint: '1 to 32. The bike returns here on release.', def: 1, min: 1, max: 32, step: '1', int: true },
  { key: 'max_level', label: 'Highest resistance level', hint: '1 to 32, at least the lowest.', def: 16, min: 1, max: 32, step: '1', int: true },
  { key: 'offset_level', label: 'Flat-road level (level at 0 % grade)', hint: 'Between lowest and highest. Free rides start here.', def: 5, min: 1, max: 32, step: '0.5' },
  { key: 'gain', label: 'Gain (levels per 1 % grade)', hint: '0 to 5.', def: 1, min: 0, max: 5, step: '0.1' },
  { key: 'downhill_scale', label: 'Downhill relief (0 to 1)', hint: 'Lower gives less relief on descents.', def: 0.5, min: 0, max: 1, step: '0.05' },
];

const KEYS = ['name', ...SPECS.map((s) => s.key)];

/** `field: problem` or `body.field: problem`, as the API reports rider validation. */
function parseFieldError(e: unknown): { field?: string; text: string } {
  if (e instanceof ApiError && e.code === 'invalid_request') {
    const m = /^(?:body\.)?([a-z_]+):\s*(.*)$/s.exec(e.message);
    if (m && KEYS.includes(m[1])) return { field: m[1], text: m[2] };
  }
  return { text: errMessage(e) };
}

function RiderForm({
  rider,
  onDone,
}: {
  rider?: Rider;
  onDone: (saved?: Rider) => void;
}) {
  const qc = useQueryClient();
  const [name, setName] = useState(rider?.name ?? '');
  const [vals, setVals] = useState<Record<Num, string>>(
    () => Object.fromEntries(SPECS.map((s) => [s.key, String(rider?.[s.key] ?? s.def)])) as Record<Num, string>,
  );
  const [err, setErr] = useState<{ field?: string; text: string } | null>(null);

  const save = useMutation({
    mutationFn: async () => {
      const nums = {} as Record<Num, number>;
      for (const s of SPECS) {
        const n = Number(vals[s.key]);
        if (vals[s.key].trim() === '' || !Number.isFinite(n)) throw new ApiError(400, 'invalid_request', `${s.key}: Enter a number.`);
        nums[s.key] = n;
      }
      if (!rider) {
        return unwrap(await api.POST('/api/riders', { body: { name: name.trim(), ...nums } }));
      }
      const body: Record<string, string | number> = {};
      if (name.trim() !== rider.name) body.name = name.trim();
      for (const s of SPECS) if (nums[s.key] !== rider[s.key]) body[s.key] = nums[s.key];
      if (Object.keys(body).length === 0) return rider;
      return unwrap(await api.PATCH('/api/riders/{id}', { params: { path: { id: rider.id } }, body }));
    },
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: RIDERS_KEY });
      onDone(r);
    },
    onError: (e) => setErr(parseFieldError(e)),
  });

  const fe = (k: string) => (err?.field === k ? err.text : undefined);
  const banner = err && !(err.field && KEYS.includes(err.field)) ? err.text : null;

  return (
    <form
      noValidate
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        setErr(null);
        save.mutate();
      }}
    >
      <Field
        label="Name"
        value={name}
        maxLength={64}
        onChange={(e) => setName(e.target.value)}
        error={fe('name')}
      />
      <div className="grid gap-4 sm:grid-cols-2">
        {SPECS.map((s) => (
          <Field
            key={s.key}
            label={s.label}
            hint={s.hint}
            type="number"
            inputMode="decimal"
            min={s.min}
            max={s.max}
            step={s.step}
            value={vals[s.key]}
            onChange={(e) => setVals((p) => ({ ...p, [s.key]: e.target.value }))}
            error={fe(s.key)}
          />
        ))}
      </div>
      {banner && <Banner tone="error">{banner}</Banner>}
      <div className="flex flex-wrap gap-3">
        <Button type="submit" variant="primary" disabled={save.isPending || !name.trim()}>
          {rider ? 'Save' : 'Create rider'}
        </Button>
        <Button onClick={() => onDone()}>Cancel</Button>
      </div>
    </form>
  );
}

function RiderCard({
  r,
  only,
  selected,
  onSelect,
}: {
  r: Rider;
  only: boolean;
  selected: boolean;
  onSelect: () => void;
}) {
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [delError, setDelError] = useState<string | null>(null);
  const del = useMutation({
    mutationFn: async () => unwrap(await api.DELETE('/api/riders/{id}', { params: { path: { id: r.id } } })),
    onSuccess: () => {
      if (getSelectedRiderId() === r.id) clearSelectedRiderId();
      void qc.invalidateQueries({ queryKey: RIDERS_KEY });
    },
    onError: (e) => {
      setConfirm(false);
      if (e instanceof ApiError && e.code === 'rider_in_use') setDelError('Release the bike first');
      else if (e instanceof ApiError && e.code === 'last_rider') setDelError('The last rider cannot be deleted.');
      else setDelError(errMessage(e));
    },
  });

  return (
    <Card className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <div className="min-w-0 flex-1 basis-48">
          <h2 className="numeral truncate text-xl">{r.name}</h2>
          <p className="text-sm text-dim">
            {r.weight_kg} kg · FTP {r.ftp_w} W · levels {r.min_level} to {r.max_level}
          </p>
        </div>
        <Button
          variant={selected ? 'primary' : 'secondary'}
          disabled={selected}
          aria-label={`${selected ? 'Using' : 'Use'} ${r.name}`}
          onClick={onSelect}
        >
          {selected && <Check aria-hidden className="size-5" />}
          {selected ? 'In use here' : 'Use here'}
        </Button>
        <Button aria-label={`${editing ? 'Close' : 'Edit'} ${r.name}`} onClick={() => setEditing((v) => !v)}>
          {editing ? 'Close' : 'Edit'}
        </Button>
        <Button
          variant={confirm ? 'danger' : 'secondary'}
          disabled={only || del.isPending}
          aria-label={confirm ? undefined : `Delete ${r.name}`}
          title={only ? 'The last rider cannot be deleted' : undefined}
          onClick={() => {
            setDelError(null);
            if (confirm) del.mutate();
            else setConfirm(true);
          }}
        >
          {confirm ? 'Click again to confirm' : 'Delete'}
        </Button>
      </div>
      {delError && <Banner tone="error">{delError}</Banner>}
      {editing && <RiderForm rider={r} onDone={() => setEditing(false)} />}
    </Card>
  );
}

export function RidersPage() {
  const q = useQuery<Rider[]>({
    queryKey: RIDERS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/riders')),
  });
  const [creating, setCreating] = useState(false);
  const [chosen, setChosen] = useState<number | null>(getSelectedRiderId);

  const riders = q.data ?? [];
  // A stale or missing choice falls back to the first rider.
  const selectedId = riders.some((r) => r.id === chosen) ? chosen : (riders[0]?.id ?? null);

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="numeral flex-1 text-2xl">Riders</h1>
        <Button variant="primary" onClick={() => setCreating(true)} disabled={creating}>
          <Plus aria-hidden className="size-5" />
          New rider
        </Button>
      </div>
      {creating && (
        <Card>
          <h2 className="numeral mb-4 text-xl">New rider</h2>
          <RiderForm
            onDone={(saved) => {
              setCreating(false);
              if (saved) {
                setSelectedRiderId(saved.id);
                setChosen(saved.id);
              }
            }}
          />
        </Card>
      )}
      {q.isPending ? (
        <p role="status" className="text-dim">
          Loading…
        </p>
      ) : q.isError ? (
        <Banner tone="error">Couldn't load riders.</Banner>
      ) : (
        riders.map((r) => (
          <RiderCard
            key={r.id}
            r={r}
            only={riders.length === 1}
            selected={r.id === selectedId}
            onSelect={() => {
              setSelectedRiderId(r.id);
              setChosen(r.id);
            }}
          />
        ))
      )}
    </div>
  );
}
