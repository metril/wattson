import { useEffect, useRef } from 'react';

/** Elements that handle Enter/Space (or arrows) themselves. */
function ownsKey(el: EventTarget | null, key: string): boolean {
  if (!(el instanceof HTMLElement)) return false;
  if (el.isContentEditable) return true;
  const tag = el.tagName;
  if (tag === 'SELECT' || tag === 'TEXTAREA' || tag === 'INPUT') return true;
  // A focused button or link already turns Enter/Space into a click.
  return (key === 'Enter' || key === ' ') && (tag === 'BUTTON' || tag === 'A');
}

/**
 * Window-level key handler for the ride screen (arrows and Enter on a TV
 * remote). Returns true from the handler when it used the key. `repeat` is
 * true for auto-repeat while a key is held.
 */
export function useRideKey(handler: (key: string, repeat: boolean) => boolean): void {
  const ref = useRef(handler);
  ref.current = handler;
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.defaultPrevented || ev.altKey || ev.ctrlKey || ev.metaKey) return;
      if (ownsKey(ev.target, ev.key)) return;
      if (ref.current(ev.key, ev.repeat)) ev.preventDefault();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
}
