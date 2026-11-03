/** Look-around / orbit offsets: a yaw in degrees (-180, 180] that eases back to 0 after the last input. */

export const SNAP_DELAY_MS = 5000;
export const ARROW_STEP_DEG = 15;
const STEP_MS = 16;
const TAU_S = 0.3;

export const wrap180 = (a: number) => {
  const w = ((((a + 180) % 360) + 360) % 360) - 180;
  return w === -180 ? 180 : w;
};

/** One snap-back step: exponential towards 0, landing exactly on 0 when close. */
export function decay(yaw: number, dtS: number, tauS = TAU_S): number {
  const next = yaw * Math.exp(-dtS / tauS);
  return Math.abs(next) < 0.5 ? 0 : next;
}

/** Dragging dxPx right across widthPx turns the view left by that share of fovDeg (the scene follows the finger). */
export function dragDeg(dxPx: number, widthPx: number, fovDeg: number): number {
  return widthPx > 0 ? (-dxPx / widthPx) * fovDeg : 0;
}

export type Look = {
  readonly yaw: number;
  nudge(deg: number): void;
  /** Back to forward at once. */
  reset(): void;
  dispose(): void;
};

/**
 * Holds the yaw offset. Every nudge restarts a SNAP_DELAY_MS timer; when it fires the
 * offset eases to 0 (or jumps there with reduced motion). apply() is called on each change.
 */
export function createLook(o: { apply(yaw: number): void; reduced(): boolean; delayMs?: number }): Look {
  let yaw = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const stop = () => {
    clearTimeout(timer);
    timer = undefined;
  };
  const set = (v: number) => {
    if (v === yaw) return;
    yaw = v;
    o.apply(yaw);
  };
  let last = 0;
  // Real elapsed time, so a busy main thread (software WebGL) slows the frames, not the easing.
  const ease = () => {
    const now = Date.now();
    set(decay(yaw, Math.min(Math.max(now - last, STEP_MS), 1000) / 1000));
    last = now;
    timer = yaw === 0 ? undefined : setTimeout(ease, STEP_MS);
  };
  const snap = () => {
    if (o.reduced()) {
      timer = undefined;
      set(0);
    } else {
      last = Date.now();
      ease();
    }
  };
  return {
    get yaw() {
      return yaw;
    },
    nudge(deg) {
      stop();
      set(wrap180(yaw + deg));
      timer = setTimeout(snap, o.delayMs ?? SNAP_DELAY_MS);
    },
    reset() {
      stop();
      set(0);
    },
    dispose: stop,
  };
}

/**
 * Horizontal drag and Left/Right arrows on `el` turn the look. Arrows are consumed only
 * while `el` itself has focus and `enabled()` is true, so the D-pad still leaves it
 * with Up/Down and every other control keeps its keys. Returns the cleanup.
 */
export function attachLookInput(
  el: HTMLElement,
  look: Look,
  o: { enabled(): boolean; fovDeg: number; stepDeg?: number },
): () => void {
  let id = -1;
  let lastX = 0;
  const down = (e: PointerEvent) => {
    if (!o.enabled() || (e.pointerType === 'mouse' && e.button !== 0)) return;
    id = e.pointerId;
    lastX = e.clientX;
    try {
      el.setPointerCapture?.(id);
    } catch {
      /* the pointer is already gone */
    }
  };
  const move = (e: PointerEvent) => {
    if (e.pointerId !== id) return;
    const dx = e.clientX - lastX;
    lastX = e.clientX;
    if (dx) look.nudge(dragDeg(dx, el.clientWidth || el.getBoundingClientRect().width, o.fovDeg));
  };
  const up = (e: PointerEvent) => {
    if (e.pointerId === id) id = -1;
  };
  const key = (e: KeyboardEvent) => {
    if (e.target !== el || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
    if (!o.enabled()) return;
    e.preventDefault();
    look.nudge((e.key === 'ArrowRight' ? 1 : -1) * (o.stepDeg ?? ARROW_STEP_DEG));
  };
  el.addEventListener('pointerdown', down);
  el.addEventListener('pointermove', move);
  el.addEventListener('pointerup', up);
  el.addEventListener('pointercancel', up);
  el.addEventListener('keydown', key);
  return () => {
    el.removeEventListener('pointerdown', down);
    el.removeEventListener('pointermove', move);
    el.removeEventListener('pointerup', up);
    el.removeEventListener('pointercancel', up);
    el.removeEventListener('keydown', key);
  };
}
