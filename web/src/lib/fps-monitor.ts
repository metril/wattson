export type FpsOpts = { warmupMs?: number; windowMs?: number; threshold?: number; badWindows?: number };

/**
 * Watches requestAnimationFrame timestamps. After a warm-up it measures whole
 * windows; consecutive windows below the threshold fire onDegrade once per
 * arm(). A pause (tab hidden, or a gap over 1 s such as a screensaver)
 * restarts the current window and keeps the bad-window count; shorter gaps
 * while visible are genuinely slow frames and count as such.
 */
export class FpsMonitor {
  private o: Required<FpsOpts>;
  private subs = new Set<(fps: number) => void>();
  private t0 = -1;
  private winStart = -1;
  private last = -1;
  private count = 0;
  private bad = 0;
  private fired = false;
  private warm = false;

  constructor(o: FpsOpts = {}) {
    this.o = { warmupMs: 8000, windowMs: 5000, threshold: 24, badWindows: 2, ...o };
  }

  onDegrade(fn: (fps: number) => void): void {
    this.subs.add(fn);
  }

  /** Starts a fresh warm-up and allows another onDegrade. */
  arm(): void {
    this.t0 = this.winStart = this.last = -1;
    this.count = this.bad = 0;
    this.fired = this.warm = false;
  }

  frame(now: number): void {
    if (this.t0 < 0) {
      this.t0 = this.winStart = this.last = now;
      return;
    }
    const gap = now - this.last;
    if (gap > 1000 || (gap > 250 && typeof document !== 'undefined' && document.hidden)) {
      this.winStart = now;
      this.count = 0;
      this.last = now;
      return;
    }
    this.last = now;
    this.count++;
    const span = now - this.winStart;
    if (!this.warm) {
      if (now - this.t0 >= this.o.warmupMs) {
        this.warm = true;
        this.winStart = now;
        this.count = 0;
      }
      return;
    }
    if (span < this.o.windowMs) return;
    const fps = (this.count * 1000) / span;
    this.bad = fps < this.o.threshold ? this.bad + 1 : 0;
    this.winStart = now;
    this.count = 0;
    if (this.bad >= this.o.badWindows && !this.fired) {
      this.fired = true;
      for (const fn of this.subs) fn(fps);
    }
  }
}
