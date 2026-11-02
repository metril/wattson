import { ApiError } from './api';
import { errMessage } from './errors';

describe('errMessage', () => {
  it('maps bike_busy and passes ApiError messages through', () => {
    expect(errMessage(new ApiError(409, 'bike_busy', 'x'))).toMatch(/Release the bike/);
    expect(errMessage(new ApiError(400, 'bad', 'nope'))).toBe('nope');
  });
  it('only a network TypeError reads as unreachable', () => {
    expect(errMessage(new TypeError('Failed to fetch'))).toBe("Couldn't reach the server.");
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    expect(errMessage(new RangeError('boom'))).toBe('boom');
    expect(spy).toHaveBeenCalled();
    spy.mockRestore();
  });
});
