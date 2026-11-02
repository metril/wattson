import { chartGeometry } from './chart-geometry';

describe('chartGeometry', () => {
  it('maps the ends of the data to the padded box', () => {
    const xs = [0, 1, 2, 4];
    const ys = [10, 50, 30, 20];
    const g = chartGeometry(xs, ys, 300, 100, 10);
    expect(g.x(xs[0])).toBe(10);
    expect(g.x(xs[3])).toBe(290);
    expect(g.y(50)).toBe(10);
    expect(g.y(10)).toBe(90);
    expect([g.min, g.max]).toEqual([10, 50]);
  });

  it('centres a flat series', () => {
    const g = chartGeometry([0, 1, 2], [7, 7, 7], 300, 100, 10);
    expect(g.y(7)).toBe(50);
  });

  it('stays finite for empty data', () => {
    const g = chartGeometry([], [], 300, 100, 10);
    for (const v of [g.x(0), g.x(5), g.y(0), g.y(5), g.min, g.max]) expect(Number.isFinite(v)).toBe(true);
    expect(g.y(3)).toBe(50);
  });
});
