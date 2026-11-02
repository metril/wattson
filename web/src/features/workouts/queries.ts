import type { RiderLite } from '@/lib/workout-math';

export const WORKOUTS_KEY = ['workouts'] as const;
export const workoutKey = (id: number) => ['workouts', id] as const;

/** Stand-in for the preview before any rider has loaded. */
export const DEFAULT_RIDER: RiderLite = { offset_level: 5, min_level: 1, max_level: 16, ftp_w: 200 };

export const riderLite = (r: RiderLite): RiderLite => ({
  offset_level: r.offset_level,
  min_level: r.min_level,
  max_level: r.max_level,
  ftp_w: r.ftp_w,
});
