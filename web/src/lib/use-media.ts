import { useEffect, useState } from 'react';

/** Live match of a CSS media query. */
export function useMedia(query: string): boolean {
  const [on, setOn] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const mq = window.matchMedia(query);
    const fn = () => setOn(mq.matches);
    fn();
    mq.addEventListener('change', fn);
    return () => mq.removeEventListener('change', fn);
  }, [query]);
  return on;
}

/**
 * Phone portrait, a phone on its side, or a tablet on its side (the right-hand
 * rail is short): the ride screen shows four metrics and a "More metrics" button.
 */
export const COMPACT_QUERY =
  '(max-width: 599px), (max-height: 499px), (min-width: 900px) and (max-width: 1599px) and (orientation: landscape)';
