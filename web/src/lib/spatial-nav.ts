import { useEffect, useRef, type RefObject } from 'react';

export type Dir = 'up' | 'down' | 'left' | 'right';

type Rect = Pick<DOMRect, 'left' | 'right' | 'top' | 'bottom' | 'width' | 'height'>;

/**
 * The best candidate to move focus to from `from` in direction `dir`, or null.
 * Candidates must lie strictly in the half-plane of `dir` (by centre). The score
 * is the gap along the direction plus twice the sideways offset of the centres,
 * so a slightly farther control in line wins over a near one off to the side.
 * Ties go to the earlier candidate (DOM order).
 */
export function nextFocus(from: Rect, candidates: { el: Element; rect: Rect }[], dir: Dir): Element | null {
  const fx = from.left + from.width / 2;
  const fy = from.top + from.height / 2;
  let best: Element | null = null;
  let bestScore = Infinity;
  for (const { el, rect } of candidates) {
    const cx = rect.left + rect.width / 2;
    const cy = rect.top + rect.height / 2;
    let gap: number;
    let side: number;
    switch (dir) {
      case 'right':
        if (cx <= fx) continue;
        gap = Math.max(0, rect.left - from.right);
        side = Math.abs(cy - fy);
        break;
      case 'left':
        if (cx >= fx) continue;
        gap = Math.max(0, from.left - rect.right);
        side = Math.abs(cy - fy);
        break;
      case 'down':
        if (cy <= fy) continue;
        gap = Math.max(0, rect.top - from.bottom);
        side = Math.abs(cx - fx);
        break;
      default:
        if (cy >= fy) continue;
        gap = Math.max(0, from.top - rect.bottom);
        side = Math.abs(cx - fx);
    }
    const score = gap + 2 * side;
    if (score < bestScore) {
      bestScore = score;
      best = el;
    }
  }
  return best;
}

const DIRS: Record<string, Dir> = { ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'left', ArrowRight: 'right' };

// Inputs that keep the arrow keys (cursor, value, radio group) or the Backspace key.
const BUTTON_LIKE = new Set(['button', 'submit', 'reset', 'checkbox', 'image', 'file']);

function ownsArrows(el: EventTarget | null, dir: Dir): boolean {
  if (!(el instanceof HTMLElement)) return false;
  if (el.isContentEditable) return true;
  // A slider keeps left/right for its value; up/down still leave it.
  if (el instanceof HTMLInputElement && el.type === 'range') return dir === 'left' || dir === 'right';
  // A select keeps up/down (its value); left/right leave it.
  if (el instanceof HTMLSelectElement) return dir === 'up' || dir === 'down';
  if (el instanceof HTMLInputElement) return !BUTTON_LIKE.has(el.type);
  return el instanceof HTMLTextAreaElement;
}

function isTextEntry(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  if (el.isContentEditable) return true;
  if (el instanceof HTMLInputElement) return !BUTTON_LIKE.has(el.type) && el.type !== 'radio' && el.type !== 'range';
  return el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement;
}

/** Enabled, visible `[data-nav]` elements under `root`, in DOM order. */
function targets(root: HTMLElement): { el: HTMLElement; rect: DOMRect }[] {
  const all = Array.from(root.querySelectorAll<HTMLElement>('[data-nav]')).filter(
    (el) => !el.matches(':disabled, [aria-disabled="true"]'),
  );
  // Hidden elements have no boxes; with no layout engine at all (jsdom) nothing does, so keep them all.
  const shown = all.filter((el) => el.getClientRects().length > 0);
  return (shown.length > 0 ? shown : all).map((el) => ({ el, rect: el.getBoundingClientRect() }));
}

/**
 * Arrow keys move focus among the visible, enabled `[data-nav]` elements inside
 * `root` (TV remote, keyboard). Escape or Backspace go back in history, unless
 * focus is in a text field or a menu already used the key (it called
 * preventDefault). Arrows left to an input, select or radio group are not taken.
 * With no layout (tests) the order of the elements stands in for geometry.
 */
export function useSpatialNav(root: RefObject<HTMLElement>, o: { enabled?: (e: KeyboardEvent) => boolean } = {}): void {
  const enabled = useRef(o.enabled);
  enabled.current = o.enabled;

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
      const box = root.current;
      if (!box || (enabled.current && !enabled.current(e))) return;

      if (e.key === 'Escape' || e.key === 'Backspace') {
        if (e.key === 'Escape' && e.target instanceof HTMLElement && isTextEntry(e.target) && !(e.target instanceof HTMLSelectElement)) {
          // Leave a text/number field for the next navigable control (arrows stay native inside it).
          const items = targets(box).filter((i) => i.el !== e.target);
          const here = e.target;
          const after = items.find((i) => here.compareDocumentPosition(i.el) & Node.DOCUMENT_POSITION_FOLLOWING);
          const out = after?.el ?? items[items.length - 1]?.el;
          if (out) {
            e.preventDefault();
            out.focus();
          }
          return;
        }
        if (isTextEntry(e.target)) return;
        e.preventDefault();
        history.back();
        return;
      }
      const dir = DIRS[e.key];
      if (!dir || ownsArrows(e.target, dir)) return;

      const items = targets(box);
      if (items.length === 0) return;
      const active = document.activeElement;
      // Focus elsewhere in the app (nav bar, More menu) belongs to the enclosing instance.
      if (active && active !== document.body && active !== document.documentElement && !box.contains(active)) return;
      const at = items.findIndex((i) => i.el === active);
      let next: Element | null = null;
      if (at < 0) {
        next = items[0].el;
      } else if (items.every((i) => i.rect.width === 0 && i.rect.height === 0)) {
        // No layout engine: treat the DOM order as a line.
        const step = dir === 'right' || dir === 'down' ? 1 : -1;
        next = items[at + step]?.el ?? null;
      } else {
        next = nextFocus(items[at].rect, items.filter((_, i) => i !== at), dir);
        if (!next && (dir === 'left' || dir === 'right')) {
          // Nothing sideways: carry on in reading order so every control stays reachable.
          const step = dir === 'right' ? 1 : -1;
          next = items[(at + step + items.length) % items.length].el;
        }
      }
      if (next instanceof HTMLElement && next !== active) {
        e.preventDefault();
        next.focus();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [root]);
}
