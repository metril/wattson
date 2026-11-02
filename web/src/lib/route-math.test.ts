import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { levelFor, distAt, gradeAt, indexAt, positionAt, previewLevels, syntheticProfile, type Profile } from './route-math';

const golden = JSON.parse(
  readFileSync(resolve(import.meta.dirname, '../../../internal/engine/testdata/mapper_golden.json'), 'utf8'),
) as {
  gain: number;
  offset: number;
  downhill_scale: number;
  min: number;
  max: number;
  grade_pct: number;
  bias: number;
  want: number;
}[];

const prof = (over: Partial<Profile> = {}): Profile => ({
  route_id: 1,
  step_m: 10,
  length_m: 25,
  lon: [0, 1, 2],
  lat: [0, 0, 0],
  ele_m: [100, 100, 100],
  grade_pct: [0, 5, 10],
  ...over,
});

describe('levelFor (shared golden file with the Go mapper)', () => {
  it('has cases', () => expect(golden.length).toBeGreaterThan(5));
  golden.forEach((c, i) => {
    it(`case ${i}: grade ${c.grade_pct} bias ${c.bias}`, () => {
      const r = { gain: c.gain, offset_level: c.offset, downhill_scale: c.downhill_scale, min_level: c.min, max_level: c.max };
      expect(Math.abs(levelFor(c.grade_pct, r, c.bias) - c.want)).toBeLessThan(1e-9);
    });
  });
  it('NaN yields the lower bound', () => {
    const r = { gain: 1, offset_level: 5, downhill_scale: 0.5, min_level: 0, max_level: 16 };
    expect(levelFor(NaN, r)).toBe(1);
  });
});

describe('profile sampling', () => {
  const p = prof();
  it('interpolates position and grade', () => {
    expect(positionAt(p, 15)).toEqual([1.5, 0]);
    expect(positionAt(p, 25)).toEqual([2, 0]);
    expect(positionAt(p, -5)).toEqual([0, 0]);
    expect(gradeAt(p, 5)).toBe(2.5);
    expect(gradeAt(p, 999)).toBe(10);
  });
  it('clamps the index and caps the distance at length', () => {
    expect(indexAt(p, 999)).toBe(2);
    expect(indexAt(p, -1)).toBe(0);
    expect(indexAt(p, 10)).toBe(1);
    expect(distAt(p, 2)).toBe(20);
    expect(distAt(prof({ length_m: 15 }), 2)).toBe(15);
  });
  it('previews levels per sample', () => {
    const r = { gain: 1, offset_level: 5, downhill_scale: 0.5, min_level: 1, max_level: 16 };
    expect(Array.from(previewLevels(p, r))).toEqual([5, 10, 15]);
  });
});

describe('syntheticProfile', () => {
  it('builds 10 m points from 100 m', () => {
    const s = syntheticProfile([
      { length_m: 100, grade_pct: 0 },
      { length_m: 200, grade_pct: 5 },
    ]);
    expect(s.distance[0]).toBe(0);
    expect(s.ele[0]).toBe(100);
    expect(s.distance.at(-1)).toBe(300);
    expect(s.ele.at(-1)).toBeCloseTo(110, 9);
    expect(s.distance).toHaveLength(31);
  });
  it('handles a segment that is not a multiple of 10', () => {
    const s = syntheticProfile([{ length_m: 25, grade_pct: 4 }]);
    expect(s.distance).toEqual([0, 10, 20, 25]);
    expect(s.ele[3]).toBeCloseTo(101, 9);
  });
  it('skips invalid segments', () => {
    expect(syntheticProfile([{ length_m: NaN, grade_pct: 1 }]).distance).toEqual([0]);
  });
});
