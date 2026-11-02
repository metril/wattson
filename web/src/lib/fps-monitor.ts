type Opts = { warmupMs?: number; windowMs?: number; threshold?: number; badWindows?: number };

/**
 * Watches requestAnimationFrame timestamps. After a warm-up it measures whole
 * windows; consecutive windows below the threshold fire onDegrade once per
 * arm(). A gap over 250 ms (hidden tab, screensaver) restarts the window
 * instead of counting as slow frames.
 */
export class FpsMonitor {
  private o: Required<Opts>;
  private subs = new Set<(fps: number) => void>();
  private t0 = -1;
  private winStart = -1;
  private last = -1;
  private count = 0;
  private bad = 0;
  private fired = false;
  private warm = false;
  private _fps = 0;

  constructor(o: Opts = {}) {
    this.o = { warmupMs: 8000, windowMs: 5000, threshold: 24, badWindows: 2, ...o };
  }

  get fps(): number {
    return this._fps;
  }

  onDegrade(fn: (fps: number) => void): () => void {
    this.subs.add(fn);
    return () => this.subs.delete(fn);
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
    if (now - this.last > 250) {
      this.winStart = now;
      this.count = 0;
      this.bad = 0;
      this.last = now;
      return;
    }
    this.last = now;
    this.count++;
    const span = now - this.winStart;
    if (!this.warm) {
      if (now - this.t0 >= this.o.warmupMs) {
        if (span > 0) this._fps = (this.count * 1000) / span;
        this.warm = true;
        this.winStart = now;
        this.count = 0;
      } else if (span >= 1000) {
        this._fps = (this.count * 1000) / span;
      }
      return;
    }
    if (span < this.o.windowMs) return;
    this._fps = (this.count * 1000) / span;
    this.bad = this._fps < this.o.threshold ? this.bad + 1 : 0;
    this.winStart = now;
    this.count = 0;
    if (this.bad >= this.o.badWindows && !this.fired) {
      this.fired = true;
      for (const fn of this.subs) fn(this._fps);
    }
  }
}
