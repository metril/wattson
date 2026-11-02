import { profileGeometry } from './profile-geometry';

describe('profileGeometry', () => {
  const p = { distance: [0, 50, 100], ele: [10, 30, 20] };
  it('maps distance and elevation to the padded box', () => {
    const g = profileGeometry(p, 400, 100, 10);
    expect(g.x(0)).toBe(10);
    expect(g.x(100)).toBe(390);
    expect(g.y(30)).toBe(10);
    expect(g.y(10)).toBe(90);
    expect(g.minEle).toBe(10);
    expect(g.maxEle).toBe(30);
  });
  it('centres a flat profile and never yields NaN', () => {
    const g = profileGeometry({ distance: [0, 10], ele: [5, 5] }, 200, 80, 8);
    expect(g.y(5)).toBe(40);
    expect(Number.isNaN(g.y(7))).toBe(false);
  });
  it('survives zero length and empty input', () => {
    const g = profileGeometry({ distance: [0], ele: [3] }, 200, 80, 8);
    expect(g.x(0)).toBe(8);
    expect(Number.isNaN(g.y(3))).toBe(false);
    const e = profileGeometry({ distance: [], ele: [] }, 200, 80, 8);
    expect(Number.isNaN(e.x(1))).toBe(false);
  });
});
