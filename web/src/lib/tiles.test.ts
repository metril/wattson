import { tileGrid, tileName } from './tiles';

describe('tileName', () => {
  it.each([
    [45.2, 7.9, 'E5_N45'],
    [-33.9, 18.4, 'E15_S35'],
    [51.5, -0.1, 'W5_N50'],
    [64.1, -21.9, 'W25_N60'],
    [0, 0, 'E0_N0'],
    [-0.1, -179.9, 'W180_S5'],
    [90, 180, 'E175_N85'],
  ])('(%d, %d) -> %s', (lat, lon, want) => expect(tileName(lat, lon)).toBe(want));
  it('NaN yields an empty name', () => expect(tileName(NaN, 1)).toBe(''));
});

describe('tileGrid', () => {
  const g = tileGrid();
  it('is 72 x 36 with unique names', () => {
    expect(g).toHaveLength(2592);
    expect(new Set(g.map((t) => t.name)).size).toBe(2592);
  });
  it('gives each tile its 5 degree box', () => {
    expect(g.find((t) => t.name === 'E5_N45')?.bbox).toEqual([5, 45, 10, 50]);
    expect(g.find((t) => t.name === 'W180_S90')?.bbox).toEqual([-180, -90, -175, -85]);
  });
  it('agrees with tileName at each tile centre', () => {
    for (const t of g) expect(tileName((t.bbox[1] + t.bbox[3]) / 2, (t.bbox[0] + t.bbox[2]) / 2)).toBe(t.name);
  });
});
