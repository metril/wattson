import { useEffect, useLayoutEffect, useRef, type RefObject } from 'react';
import type { Tick } from './ws-types';

/** Latest tick plus subscribers. Ticks live here, never in React state. */
export class TickStore {
  latest: Tick | null = null;
  private subs = new Set<(t: Tick) => void>();

  set(t: Tick): void {
    this.latest = t;
    for (const fn of this.subs) fn(t);
  }

  subscribe(fn: (t: Tick) => void): () => void {
    this.subs.add(fn);
    return () => this.subs.delete(fn);
  }
}

const useIsoLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect;

/**
 * A ref for a <span> whose text follows the store. Writes go straight to the
 * DOM in a requestAnimationFrame, so a tick never re-renders React.
 */
export function useTickText(store: TickStore, pick: (t: Tick) => string): RefObject<HTMLSpanElement> {
  const ref = useRef<HTMLSpanElement>(null);
  const pickRef = useRef(pick);
  pickRef.current = pick;

  useIsoLayoutEffect(() => {
    let raf = 0;
    const write = () => {
      raf = 0;
      const el = ref.current;
      const t = store.latest;
      if (el) el.textContent = t ? pickRef.current(t) : '–';
    };
    write();
    const unsub = store.subscribe(() => {
      if (!raf) raf = requestAnimationFrame(write);
    });
    return () => {
      unsub();
      if (raf) cancelAnimationFrame(raf);
    };
  }, [store]);

  return ref as RefObject<HTMLSpanElement>;
}
