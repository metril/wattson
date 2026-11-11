import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { Plus } from 'lucide-react';
import { api, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { mmss } from '@/lib/workout-math';
import { Banner } from '@/components/Banner';
import { PageHeader } from '@/components/PageHeader';
import { WorkoutChart } from '@/features/workouts/WorkoutChart';
import { DEFAULT_RIDER, riderLite, WORKOUTS_KEY } from '@/features/workouts/queries';
import { RIDERS_KEY } from './Riders';

export const TYPE_LABEL = { interval: 'Interval', erg: 'ERG' } as const;

export function WorkoutsPage() {
  const q = useQuery({
    queryKey: WORKOUTS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/workouts')),
  });
  const riders = useQuery({
    queryKey: RIDERS_KEY,
    queryFn: async () => unwrap(await api.GET('/api/riders')),
  });
  const rider = riders.data?.[0] ? riderLite(riders.data[0]) : DEFAULT_RIDER;
  const list = q.data ?? [];

  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-5">
      <PageHeader title="Workouts" subtitle="Structured intervals for ERG mode" actions={<>
        <Link
          to="/workouts/new"
          className="inline-flex min-h-touch items-center justify-center gap-2 rounded-control bg-accent px-4 font-medium text-accent-ink hover:brightness-110"
        >
          <Plus aria-hidden className="size-5" />
          New workout
        </Link>
      </>} />

      {q.isPending ? (
        <p role="status" className="text-dim">
          Loading…
        </p>
      ) : q.isError ? (
        <Banner tone="error">{errMessage(q.error)}</Banner>
      ) : list.length === 0 ? (
        <div className="rounded-panel border border-dashed border-line p-8 text-center text-dim">
          <p className="text-lg text-chalk">No workouts yet</p>
          <p>Build an interval or ERG workout from blocks, then ride it.</p>
        </div>
      ) : (
        <ul className="flex flex-col overflow-hidden rounded-panel border border-line bg-panel">
          {list.map((w) => (
            <li key={w.id} className="border-b border-line last:border-0 hover:bg-raised">
              <Link to={`/workouts/${w.id}`} className="flex min-h-14 flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2">
                <span className="min-w-0 flex-1 basis-40 truncate text-lg font-medium text-chalk">{w.name}</span>
                <span className="rounded-full border border-line px-2.5 py-0.5 text-xs text-dim">{TYPE_LABEL[w.type]}</span>
                <span className="numeral w-20 text-right text-lg">{mmss(w.duration_s)}</span>
                <WorkoutChart compact type={w.type} blocks={w.blocks} rider={rider} className="h-10 w-full sm:w-44" />
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
