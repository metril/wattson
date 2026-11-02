import { fixed0, hms, km } from './format';

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
});
