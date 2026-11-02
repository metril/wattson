import { useMemo, useRef, useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Play } from 'lucide-react';
import { api, ApiError, unwrap } from '@/lib/api';
import { useMe } from '@/lib/auth';
import { errMessage } from '@/lib/errors';
import { getSelectedRiderId } from '@/lib/rider';
import type { components } from '@/lib/openapi';
import { blockErrors, defaultBlock, MAX_TARGET_W, MIN_TARGET_W, mmss, totalS, type Block, type WorkoutType } from '@/lib/workout-math';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { Field } from '@/components/Field';
import { Select } from '@/components/Select';
import { BlockEditor } from '@/features/workouts/BlockEditor';
import { WorkoutChart } from '@/features/workouts/WorkoutChart';
import { DEFAULT_RIDER, riderLite, WORKOUTS_KEY, workoutKey } from '@/features/workouts/queries';
import { RIDERS_KEY } from './Riders';

type Workout = components['schemas']['WorkoutOut'];

const TYPES: { value: WorkoutType; label: string }[] = [
  { value: 'interval', label: 'Interval' },
  { value: 'erg', label: 'ERG' },
];

const isNotFound = (e: unknown) => e instanceof ApiError && e.status === 404;

function Gone() {
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3">
      <Banner tone="warn">This workout no longer exists. It may have been deleted.</Banner>
      <Link to="/workouts" className="inline-flex min-h-touch items-center px-1 text-accent">
        Back to workouts
      </Link>
    </div>
  );
}

const isClamped = (pct: number, ftp: number) => {
  const w = Math.round((pct * ftp) / 100);
  return w < MIN_TARGET_W || w > MAX_TARGET_W;
};

function Form({ workout }: { workout?: Workout }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const me = useMe();
  const [name, setName] = useState(workout?.name ?? '');
  const [type, setType] = useState<WorkoutType>(workout?.type ?? 'interval');
  const [blocks, setBlocks] = useState<Block[]>(workout?.blocks ?? [defaultBlock('interval')]);
  const [msg, setMsg] = useState<{ tone: 'ok' | 'error'; text: string } | null>(null);
  const [confirm, setConfirm] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => () => clearTimeout(timer.current), []);

  const riders = useQuery({
    queryKey: RIDERS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/riders')),
  });
  const list = riders.data ?? [];
  const [chosen, setChosen] = useState<number | null>(getSelectedRiderId);
  const picked = list.find((r) => r.id === chosen) ?? list[0];
  const rider = picked ? riderLite(picked) : DEFAULT_RIDER;

  const errors = useMemo(() => blockErrors(type, blocks), [type, blocks]);
  const invalid = Object.keys(errors).length > 0;
  const canEdit = !workout || (!!me.data && (me.data.user.is_admin || me.data.user.id === workout.owner_id));
  // A % FTP block outside 50..1000 W is clamped when ridden, whatever the reason (no FTP, low FTP, huge FTP).
  const clamped = type === 'erg' ? blocks.filter((b) => b.ftp_pct !== undefined && isClamped(b.ftp_pct, rider.ftp_w)) : [];

  const save = useMutation({
    mutationFn: async () => {
      const body = { name: name.trim(), type, blocks };
      return workout
        ? unwrap(await api.PUT('/api/workouts/{id}', { params: { path: { id: workout.id } }, body }))
        : unwrap(await api.POST('/api/workouts', { body }));
    },
    onSuccess: (w) => {
      void qc.invalidateQueries({ queryKey: WORKOUTS_KEY });
      qc.setQueryData(workoutKey(w.id), w);
      if (workout) setMsg({ tone: 'ok', text: 'Saved. Rides in progress keep the version they started with.' });
      else navigate(`/workouts/${w.id}`, { replace: true });
    },
    onError: (e) => setMsg({ tone: 'error', text: errMessage(e) }),
  });
  const del = useMutation({
    mutationFn: async () => {
      if (!workout) throw new Error('No workout');
      return unwrap(await api.DELETE('/api/workouts/{id}', { params: { path: { id: workout.id } } }));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: WORKOUTS_KEY });
      navigate('/workouts', { replace: true });
    },
    onError: (e) => {
      setConfirm(false);
      setMsg({
        tone: 'error',
        text:
          e instanceof ApiError && e.code === 'workout_in_use'
            ? 'This workout is in use by the current ride. Stop the ride first, then delete it.'
            : errMessage(e),
      });
    },
  });

  function switchType(t: WorkoutType) {
    if (t === type) return;
    setType(t);
    setBlocks((bs) => bs.map((b) => ({ ...defaultBlock(t), duration_s: b.duration_s })));
  }

  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="numeral min-w-0 flex-1 truncate text-2xl">{workout ? workout.name : 'New workout'}</h1>
        <Link to="/workouts" className="inline-flex min-h-touch items-center px-2 text-dim hover:text-chalk">
          Back to workouts
        </Link>
      </div>

      {me.data && !canEdit && <Banner tone="info">Only the owner or an admin can change this workout. You can still ride it.</Banner>}

      <Card className="flex flex-col gap-4">
        <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_auto]">
          <Field label="Name" value={name} maxLength={100} disabled={!canEdit} onChange={(e) => setName(e.target.value)} />
          <fieldset className="flex flex-col gap-1.5" disabled={!canEdit}>
            <legend className="mb-1.5 text-sm font-medium text-dim">Type</legend>
            <div className="flex gap-2">
              {TYPES.map((t) => (
                <label
                  key={t.value}
                  className="inline-flex min-h-touch min-w-24 cursor-pointer items-center justify-center rounded-control border border-line bg-raised px-4 font-medium has-[:checked]:border-accent has-[:checked]:bg-accent has-[:checked]:text-accent-ink has-[:focus-visible]:outline-3 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-accent"
                >
                  <input
                    type="radio"
                    name="workout-type"
                    className="sr-only"
                    checked={type === t.value}
                    onChange={() => switchType(t.value)}
                  />
                  {t.label}
                </label>
              ))}
            </div>
          </fieldset>
        </div>

        <fieldset disabled={!canEdit} className="min-w-0 border-0 p-0">
          <legend className="sr-only">Blocks</legend>
          <BlockEditor type={type} blocks={blocks} errors={errors} rider={rider} onChange={setBlocks} />
        </fieldset>
      </Card>

      <Card className="flex flex-col gap-3">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <h2 className="numeral text-xl">Preview</h2>
          <p className="numeral text-lg" aria-live="polite">
            Total {mmss(totalS(blocks))}
          </p>
        </div>
        {list.length > 0 && (
          <Select
            label="Preview for rider"
            value={picked?.id ?? ''}
            onChange={(e) => setChosen(Number(e.target.value))}
            hint={picked && `Levels ${picked.min_level} to ${picked.max_level}, flat-road level ${picked.offset_level}, FTP ${picked.ftp_w} W`}
          >
            {list.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </Select>
        )}
        {riders.isSuccess && list.length === 0 && (
          <Banner tone="info">No rider yet, so the preview uses an example rider (FTP {DEFAULT_RIDER.ftp_w} W).</Banner>
        )}
        {clamped.length > 0 && (
          <Banner tone="warn">
            {clamped.length === 1 ? '1 % FTP block falls' : `${clamped.length} % FTP blocks fall`} outside {MIN_TARGET_W} to {MAX_TARGET_W} W
            at {rider.ftp_w > 0 ? `an FTP of ${rider.ftp_w} W` : 'no FTP'} and would be raised to 50 W or capped at {MAX_TARGET_W} W. Check FTP on the Riders
            page, or use watts.
          </Banner>
        )}
        <WorkoutChart type={type} blocks={blocks} rider={rider} />
      </Card>

      {msg && <Banner tone={msg.tone}>{msg.text}</Banner>}

      <div className="flex flex-wrap gap-3">
        <Button
          variant="primary"
          disabled={!canEdit || invalid || !name.trim() || save.isPending}
          onClick={() => {
            setMsg(null);
            save.mutate();
          }}
        >
          {save.isPending ? 'Saving…' : 'Save'}
        </Button>
        {workout && (
          <Link
            to={`/?workout=${workout.id}`}
            className="inline-flex min-h-touch items-center justify-center gap-2 rounded-control border border-line bg-raised px-5 font-medium hover:border-accent"
          >
            <Play aria-hidden className="size-5" />
            Ride this workout
          </Link>
        )}
        {workout && canEdit && (
          <Button
            variant={confirm ? 'danger' : 'secondary'}
            disabled={del.isPending}
            onClick={() => {
              setMsg(null);
              if (confirm) {
                clearTimeout(timer.current);
                del.mutate();
              } else {
                setConfirm(true);
                timer.current = setTimeout(() => setConfirm(false), 4000);
              }
            }}
          >
            {confirm ? 'Tap again to delete' : 'Delete'}
          </Button>
        )}
      </div>
    </div>
  );
}

export function WorkoutEditPage() {
  const idParam = useParams().id;
  const id = Number(idParam);
  const isNew = idParam === undefined;
  const q = useQuery({
    queryKey: workoutKey(id),
    enabled: !isNew && Number.isInteger(id) && id > 0,
    retry: false,
    queryFn: async () => unwrap(await api.GET('/api/workouts/{id}', { params: { path: { id } } })),
  });

  if (isNew) return <Form />;
  if (!Number.isInteger(id) || id <= 0) return <Gone />;
  if (q.isPending) return <p role="status" className="text-dim">Loading…</p>;
  if (q.isError) return isNotFound(q.error) ? <Gone /> : <Banner tone="error">{errMessage(q.error)}</Banner>;
  // Keyed so a refetch of another workout resets the form; edits stay put while the same one refetches.
  return <Form key={q.data.id} workout={q.data} />;
}
