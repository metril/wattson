import { fmtDelta, GhostCursor, type GhostPoint } from './ghost';

const pts: GhostPoint[] = [
  [10, 5],
  [20, 10],
  [30, 20],
];

describe('GhostCursor', () => {
  it('is null before the first point and past the end', () => {
    const g = new GhostCursor(pts);
    expect(g.delta(5, 1)).toBeNull();
    expect(g.delta(31, 40)).toBeNull();
    expect(new GhostCursor([]).delta(0, 0)).toBeNull();
  });

  it('is positive behind and negative ahead, interpolating', () => {
    const g = new GhostCursor(pts);
    expect(g.delta(15, 10)).toBeCloseTo(2.5); // ghost reached 15 m at 7.5 s
    expect(g.delta(25, 10)).toBeCloseTo(-5); // ghost reached 25 m at 15 s
    expect(g.delta(30, 22)).toBe(2);
  });

  it('walks back when distance restarts', () => {
    const g = new GhostCursor(pts);
    g.delta(28, 18);
    expect(g.delta(12, 6)).toBeCloseTo(0);
  });
});

describe('fmtDelta', () => {
  it('formats', () => {
    expect(fmtDelta(3.2)).toBe('+3 s');
    expect(fmtDelta(-12)).toBe('-12 s');
    expect(fmtDelta(0.2)).toBe('0 s');
    expect(fmtDelta(-75)).toBe('-1:15');
  });
});
