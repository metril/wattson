import { nextFocus, type Dir } from './spatial-nav';

const rect = (left: number, top: number, w = 100, h = 100) =>
  ({ left, top, right: left + w, bottom: top + h, width: w, height: h }) as DOMRect;
const el = () => document.createElement('button');

// 3 columns x 2 rows, 100 px cells with 20 px gaps.
const grid = Array.from({ length: 6 }, (_, i) => ({ el: el(), rect: rect((i % 3) * 120, Math.floor(i / 3) * 120) }));
const at = (c: number, r: number) => grid[r * 3 + c];
const from = (c: number, r: number) => at(c, r).rect;
const others = (c: number, r: number) => grid.filter((g) => g !== at(c, r));
const go = (c: number, r: number, dir: Dir) => nextFocus(from(c, r), others(c, r), dir);

describe('nextFocus', () => {
  it('moves through a grid', () => {
    expect(go(0, 0, 'right')).toBe(at(1, 0).el);
    expect(go(1, 0, 'down')).toBe(at(1, 1).el);
    expect(go(1, 1, 'left')).toBe(at(0, 1).el);
    expect(go(2, 1, 'up')).toBe(at(2, 0).el);
  });

  it('returns null at the edge', () => {
    expect(go(2, 0, 'right')).toBeNull();
    expect(go(0, 0, 'left')).toBeNull();
    expect(go(0, 0, 'up')).toBeNull();
    expect(go(0, 1, 'down')).toBeNull();
  });

  it('prefers a farther aligned control over a near diagonal one', () => {
    const aligned = { el: el(), rect: rect(400, 0) };
    const diagonal = { el: el(), rect: rect(120, 150) };
    expect(nextFocus(rect(0, 0), [diagonal, aligned], 'right')).toBe(aligned.el);
  });

  it('only looks into the half-plane of the direction', () => {
    const behind = { el: el(), rect: rect(-300, 0) };
    expect(nextFocus(rect(0, 0), [behind], 'right')).toBeNull();
    expect(nextFocus(rect(0, 0), [behind], 'left')).toBe(behind.el);
  });

  it('breaks ties in DOM order', () => {
    const a = { el: el(), rect: rect(200, -50) };
    const b = { el: el(), rect: rect(200, 50) };
    expect(nextFocus(rect(0, 0), [a, b], 'right')).toBe(a.el);
    expect(nextFocus(rect(0, 0), [b, a], 'right')).toBe(b.el);
  });
});
