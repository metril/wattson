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
 * ArrowUp/ArrowDown change the level only while focus is on the page itself or on
 * a ride control button (Start/Pause/Resume/Stop, marked `data-level`); everywhere
 * else the arrows move focus. The - / + buttons are deliberately not marked: a
 * button that swallowed Up/Down would trap the D-pad.
 */
export function isLevelFocus(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return true;
  return target === document.body || target === document.documentElement || !!target.closest('[data-level]');
}

/**
 * Window-level (capture) key handler for the ride screen (arrows and Enter on a TV
 * remote). Returns true from the handler when it used the key. `repeat` is
 * true for auto-repeat while a key is held.
 */
export function useRideKey(handler: (key: string, repeat: boolean, target: EventTarget | null) => boolean): void {
  const ref = useRef(handler);
  ref.current = handler;
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.defaultPrevented || ev.altKey || ev.ctrlKey || ev.metaKey) return;
      if (ownsKey(ev.target, ev.key)) return;
      if (ref.current(ev.key, ev.repeat, ev.target)) ev.preventDefault();
    };
    // Capture phase: runs before the navigation handlers whatever the mount order.
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, []);
}
