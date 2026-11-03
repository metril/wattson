import { photoAt, upcoming, type Photo } from './photo-select';

const p = (d_m: number): Photo => ({ id: `p${d_m}`, d_m, bearing_deg: 0, route_bearing_deg: 0, captured_at: 0, lat: 0, lon: 0, pano: false });
const photos = [0, 15, 30, 200].map(p);

describe('photoAt', () => {
  it('takes the last photo within 10 m ahead', () => {
    expect(photoAt(photos, 22)).toBe(2); // 30 <= 32
    expect(photoAt(photos, 3)).toBe(0); // 15 > 13
    expect(photoAt(photos, 20.1)).toBe(2);
    expect(photoAt(photos, 19.9)).toBe(1);
  });
  it('is -1 when the nearest photo behind is too far, or there are none', () => {
    expect(photoAt(photos, 120)).toBe(-1); // gap 90 > 60
    expect(photoAt([], 5)).toBe(-1);
    expect(photoAt([p(100)], 50)).toBe(-1); // nothing at or before d + 10
  });
  it('keeps the last photo at the end of the route', () => {
    expect(photoAt(photos, 230)).toBe(3);
    expect(photoAt(photos, 300)).toBe(-1);
  });
  it('honours custom look-ahead and gap', () => {
    expect(photoAt(photos, 3, 0)).toBe(0);
    expect(photoAt(photos, 120, 10, 100)).toBe(2);
  });
});

describe('upcoming', () => {
  it('lists the next photos after the index', () => {
    expect(upcoming(photos, 1).map((x) => x.id)).toEqual(['p30', 'p200']);
    expect(upcoming(photos, 0, 2).map((x) => x.id)).toEqual(['p15', 'p30']);
    expect(upcoming(photos, 3)).toEqual([]);
    expect(upcoming(photos, -1, 1).map((x) => x.id)).toEqual(['p0']);
  });
});
