import { FpsMonitor } from './fps-monitor';

function run(m: FpsMonitor, from: number, to: number, dt: number): number {
  let t = from;
  for (; t <= to; t += dt) m.frame(t);
  return t;
}

describe('FpsMonitor', () => {
  it('60 fps never degrades', () => {
    const m = new FpsMonitor();
    const fn = vi.fn();
    m.onDegrade(fn);
    run(m, 0, 20000, 1000 / 60);
    expect(fn).not.toHaveBeenCalled();
    expect(m.fps).toBeGreaterThan(55);
    expect(m.fps).toBeLessThan(65);
  });

  it('20 fps: quiet during warm-up, one call at about 18 s', () => {
    const m = new FpsMonitor();
    const calls: number[] = [];
    let now = 0;
    m.onDegrade(() => calls.push(now));
    for (now = 0; now <= 30000; now += 50) {
      m.frame(now);
      if (now < 17900) expect(calls).toHaveLength(0);
    }
    expect(calls).toHaveLength(1);
    expect(calls[0]).toBeGreaterThanOrEqual(17900);
    expect(calls[0]).toBeLessThanOrEqual(18200);
  });

  it('gap over 250 ms resets the window', () => {
    const m = new FpsMonitor();
    const fn = vi.fn();
    m.onDegrade(fn);
    let t = run(m, 0, 4000, 50);
    t += 3000;
    run(m, t, t + 30000, 1000 / 60);
    expect(fn).not.toHaveBeenCalled();
  });

  it('a gap does not count as slow frames in the middle of a bad run', () => {
    const m = new FpsMonitor({ warmupMs: 0, windowMs: 1000, badWindows: 2 });
    const fn = vi.fn();
    m.onDegrade(fn);
    let t = run(m, 0, 1500, 50); // one bad window done
    t += 5000; // screensaver
    t = run(m, t, t + 600, 50);
    expect(fn).not.toHaveBeenCalled();
  });

  it('arm() re-enables a fired monitor', () => {
    const m = new FpsMonitor();
    const fn = vi.fn();
    m.onDegrade(fn);
    run(m, 0, 30000, 50);
    expect(fn).toHaveBeenCalledTimes(1);
    m.arm();
    run(m, 40000, 70000, 50);
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it('unsubscribe stops calls', () => {
    const m = new FpsMonitor();
    const fn = vi.fn();
    m.onDegrade(fn)();
    run(m, 0, 30000, 50);
    expect(fn).not.toHaveBeenCalled();
  });
});
