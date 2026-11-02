import type { components } from './openapi';

export type Photo = components['schemas']['Photo'];

/**
 * Index of the photo to show at distance d: the last one at most `aheadM`
 * metres in front of the rider, so the picture shows the road just ahead.
 * -1 when there is none, or when it is more than `maxGapM` behind.
 */
export function photoAt(photos: Photo[], d: number, aheadM = 10, maxGapM = 60): number {
  let lo = 0;
  let hi = photos.length - 1;
  let at = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (photos[mid].d_m <= d + aheadM) {
      at = mid;
      lo = mid + 1;
    } else hi = mid - 1;
  }
  return at >= 0 && d - photos[at].d_m <= maxGapM ? at : -1;
}

/** The next `n` photos after index i, for prefetching. */
export function upcoming(photos: Photo[], i: number, n = 3): Photo[] {
  return photos.slice(i + 1, i + 1 + n);
}
