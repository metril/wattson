import { profileFixture } from '@/test-utils';
import { extrapolate, routeBearing, smoothBearing } from './camera';

const norm = (d: number) => ((d % 360) + 360) % 360;
const signed = (from: number, to: number) => ((norm(to - from) + 540) % 360) - 180;

describe('routeBearing', () => {
  it('due east is about 90', () => {
    const p = profileFixture({ length_m: 200, step_m: 100, lon: [7, 7.0013, 7.0026], lat: [45, 45, 45] });
    expect(routeBearing(p, 0)).toBeCloseTo(90, 0);
  });
  it('due north is 0 and south 180', () => {
    const n = profileFixture({ length_m: 200, step_m: 100, lon: [7, 7, 7], lat: [45, 45.001, 45.002] });
    expect(routeBearing(n, 10)).toBeCloseTo(0, 1);
    const s = profileFixture({ length_m: 200, step_m: 100, lon: [7, 7, 7], lat: [45.002, 45.001, 45] });
    expect(routeBearing(s, 10)).toBeCloseTo(180, 1);
  });
  it('zero-length segment keeps previous bearing', () => {
    const p = profileFixture({ length_m: 300, step_m: 100, lon: [7, 7.0013, 7.0013, 7.0013], lat: [45, 45, 45, 45] });
    const b = routeBearing(p, 250);
    expect(Number.isFinite(b)).toBe(true);
    expect(b).toBeCloseTo(90, 0);
  });
  it('lookahead past end uses last segment', () => {
    const p = profileFixture({ length_m: 200, step_m: 100, lon: [7, 7, 7.0013], lat: [45, 45.001, 45.001] });
    expect(routeBearing(p, 195)).toBeCloseTo(90, 0);
    expect(routeBearing(p, 200)).toBeCloseTo(90, 0);
  });
  it('a route of identical points gives a finite bearing', () => {
    const p = profileFixture({ length_m: 20, step_m: 10, lon: [7, 7, 7], lat: [45, 45, 45] });
    expect(routeBearing(p, 5)).toBe(0);
  });
});

describe('smoothBearing', () => {
  it('359 to 1 turns 2 degrees, never via 180', () => {
    const r = smoothBearing(359, 1, 10);
    const turned = signed(359, r);
    expect(turned).toBeGreaterThan(1.9);
    expect(turned).toBeLessThanOrEqual(2);
    expect(r).toBeGreaterThanOrEqual(0);
    expect(r).toBeLessThan(360);
  });
  it('caps the turn rate at 45 deg/s', () => {
    expect(smoothBearing(0, 90, 0.1)).toBeLessThanOrEqual(4.5);
    expect(smoothBearing(0, 270, 0.1)).toBeGreaterThanOrEqual(355.5);
  });
  it('ignores bad input', () => {
    expect(smoothBearing(10, NaN, 1)).toBe(10);
    expect(smoothBearing(10, 50, 0)).toBe(10);
  });
});

describe('extrapolate', () => {
  it('adds speed times time up to 1.5 s and clamps to the route', () => {
    expect(extrapolate(100, 36, 0.5, 1000)).toBe(105);
    expect(extrapolate(100, 36, 5, 1000)).toBe(115);
    expect(extrapolate(995, 36, 1, 1000)).toBe(1000);
    expect(extrapolate(100, -5, 1, 1000)).toBe(100);
  });
});
