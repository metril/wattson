import { afterEach, describe, expect, it, vi } from 'vitest';
import { attachLookInput, createLook, decay, dragDeg, wrap180 } from './look';

afterEach(() => vi.useRealTimers());

describe('look math', () => {
  it('wraps to (-180, 180]', () => {
    expect(wrap180(190)).toBe(-170);
    expect(wrap180(-190)).toBe(170);
    expect(wrap180(180)).toBe(180);
    expect(wrap180(-180)).toBe(180);
    expect(wrap180(720)).toBe(0);
  });
  it('decay shrinks and lands on 0', () => {
    expect(decay(60, 0.016)).toBeLessThan(60);
    expect(decay(60, 0.016)).toBeGreaterThan(0);
    expect(decay(-0.4, 0.016)).toBe(0);
  });
  it('dragging right turns left', () => {
    expect(dragDeg(100, 400, 120)).toBe(-30);
    expect(dragDeg(-100, 400, 120)).toBe(30);
    expect(dragDeg(5, 0, 120)).toBe(0);
  });
});

describe('createLook', () => {
  it('snaps back 5 s after the last input, animated', () => {
    vi.useFakeTimers();
    const seen: number[] = [];
    const look = createLook({ apply: (y) => seen.push(y), reduced: () => false });
    look.nudge(40);
    vi.advanceTimersByTime(4000);
    look.nudge(10); // restarts the wait
    vi.advanceTimersByTime(4900);
    expect(look.yaw).toBe(50);
    vi.advanceTimersByTime(200); // delay over: easing starts
    expect(look.yaw).toBeLessThan(50);
    expect(look.yaw).toBeGreaterThan(0);
    vi.advanceTimersByTime(3000);
    expect(look.yaw).toBe(0);
    expect(seen.length).toBeGreaterThan(5);
    expect(seen.at(-1)).toBe(0);
  });
  it('reduced motion jumps straight back', () => {
    vi.useFakeTimers();
    const seen: number[] = [];
    const look = createLook({ apply: (y) => seen.push(y), reduced: () => true });
    look.nudge(-30);
    vi.advanceTimersByTime(5001);
    expect(seen).toEqual([-30, 0]);
  });
  it('reset is immediate and cancels the timer; wraps at the seam', () => {
    vi.useFakeTimers();
    const apply = vi.fn();
    const look = createLook({ apply, reduced: () => false });
    look.nudge(170);
    look.nudge(20);
    expect(look.yaw).toBe(-170);
    look.reset();
    expect(look.yaw).toBe(0);
    apply.mockClear();
    vi.advanceTimersByTime(10_000);
    expect(apply).not.toHaveBeenCalled();
  });
});

describe('attachLookInput', () => {
  const setup = (enabled = true) => {
    const el = document.createElement('div');
    document.body.append(el);
    Object.defineProperty(el, 'clientWidth', { value: 400 });
    const look = { yaw: 0, nudge: vi.fn(), reset: vi.fn(), dispose: vi.fn() };
    const off = attachLookInput(el, look, { enabled: () => enabled, fovDeg: 120 });
    return { el, look, off };
  };
  const ptr = (type: string, x: number) => new MouseEvent(type, { clientX: x, bubbles: true }) as unknown as PointerEvent;
  it('arrows nudge and are consumed only when el has focus', () => {
    const { el, look } = setup();
    const e = new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true });
    el.dispatchEvent(e);
    expect(look.nudge).toHaveBeenCalledWith(15);
    expect(e.defaultPrevented).toBe(true);
    const child = document.createElement('button');
    el.append(child);
    const e2 = new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true, cancelable: true });
    child.dispatchEvent(e2);
    expect(e2.defaultPrevented).toBe(false);
    const e3 = new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true });
    el.dispatchEvent(e3);
    expect(e3.defaultPrevented).toBe(false);
  });
  it('disabled: arrows are left to the D-pad', () => {
    const { el, look } = setup(false);
    const e = new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true, cancelable: true });
    el.dispatchEvent(e);
    expect(e.defaultPrevented).toBe(false);
    expect(look.nudge).not.toHaveBeenCalled();
  });
  it('horizontal drag nudges by the dragged share of the fov', () => {
    const { el, look, off } = setup();
    Object.assign(el, { setPointerCapture: vi.fn() });
    const down = Object.assign(ptr('pointerdown', 100), { pointerId: 1, pointerType: 'touch' });
    el.dispatchEvent(down);
    el.dispatchEvent(Object.assign(ptr('pointermove', 140), { pointerId: 1 }));
    expect(look.nudge).toHaveBeenCalledWith(-12);
    el.dispatchEvent(Object.assign(ptr('pointerup', 140), { pointerId: 1 }));
    el.dispatchEvent(Object.assign(ptr('pointermove', 200), { pointerId: 1 }));
    expect(look.nudge).toHaveBeenCalledTimes(1);
    off();
  });
});
