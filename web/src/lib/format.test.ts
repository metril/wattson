import { distUnit, elevUnit, fixed0, fmtDist, fmtElev, fmtSpeed, hms, km, speedUnit, toDist, toElev, toSpeed } from './format';

describe('format', () => {
  it('hms pads hours, minutes and seconds', () => {
    expect(hms(65)).toBe('00:01:05');
    expect(hms(3600)).toBe('01:00:00');
    expect(hms(0)).toBe('00:00:00');
  });
  it('hms clamps negatives and non-finite values to zero', () => {
    expect(hms(-5)).toBe('00:00:00');
    expect(hms(Number.NaN)).toBe('00:00:00');
  });
  it('km converts metres to two decimals', () => {
    expect(km(2761)).toBe('2.76');
    expect(km(0)).toBe('0.00');
  });
  it('fixed0 rounds to an integer string', () => {
    expect(fixed0(129.6)).toBe('130');
    expect(fixed0(0.4)).toBe('0');
  });
  it('unit-aware formatters include the unit', () => {
    expect(fmtDist(1609.344, 'imperial')).toBe('1.00 mi');
    expect(fmtDist(2761, 'metric')).toBe('2.76 km');
    expect(fmtSpeed(100, 'metric')).toBe('100.0 km/h');
    expect(fmtSpeed(160.9344, 'imperial')).toBe('100.0 mph');
    expect(fmtElev(100, 'metric')).toBe('100 m');
    expect(fmtElev(100, 'imperial')).toBe('328 ft');
  });
  it('numeric converters and unit labels', () => {
    expect(toDist(1000, 'metric')).toBe(1);
    expect(toSpeed(36, 'metric')).toBe(36);
    expect(toElev(0.3048, 'imperial')).toBeCloseTo(1);
    expect([distUnit('imperial'), speedUnit('imperial'), elevUnit('imperial')]).toEqual(['mi', 'mph', 'ft']);
    expect([distUnit('metric'), speedUnit('metric'), elevUnit('metric')]).toEqual(['km', 'km/h', 'm']);
  });
});
