import { ApiError, api, setKioskToken, setUnauthorizedHandler, unwrap } from './api';

function stubFetch(status: number, body?: unknown) {
  const fn = vi.fn(async (_req: Request) =>
    status === 204
      ? new Response(null, { status })
      : new Response(JSON.stringify(body ?? {}), {
          status,
          headers: { 'content-type': 'application/json' },
        }),
  );
  vi.stubGlobal('fetch', fn);
  return fn;
}

afterEach(() => {
  vi.unstubAllGlobals();
  setKioskToken(null);
  setUnauthorizedHandler(() => {});
  window.history.replaceState(null, '', '/');
});

describe('api client', () => {
  it('adds kiosk bearer', async () => {
    const f = stubFetch(200, []);
    setKioskToken('wat_x');
    await api.GET('/api/riders');
    expect(f.mock.calls[0][0].headers.get('Authorization')).toBe('Bearer wat_x');
  });

  it('sends no Authorization header without a kiosk token', async () => {
    const f = stubFetch(200, []);
    await api.GET('/api/riders');
    expect(f.mock.calls[0][0].headers.has('Authorization')).toBe(false);
  });

  it('401 redirects to login with return_to', async () => {
    window.history.replaceState(null, '', '/riders?x=1');
    stubFetch(401, { error: { code: 'unauthorized', message: 'm' } });
    const h = vi.fn();
    setUnauthorizedHandler(h);
    await api.GET('/api/riders');
    expect(h).toHaveBeenCalledWith('/riders?x=1');
  });

  it('login 401 does not redirect', async () => {
    stubFetch(401, { error: { code: 'invalid_credentials', message: 'm' } });
    const h = vi.fn();
    setUnauthorizedHandler(h);
    await api.POST('/api/auth/login', { body: { username: 'a', password: 'b' } });
    expect(h).not.toHaveBeenCalled();
  });

  it('me 401 does not redirect', async () => {
    stubFetch(401, { error: { code: 'unauthorized', message: 'm' } });
    const h = vi.fn();
    setUnauthorizedHandler(h);
    await api.GET('/api/auth/me');
    expect(h).not.toHaveBeenCalled();
  });
});

describe('unwrap', () => {
  it('throws ApiError with status and code', async () => {
    stubFetch(409, { error: { code: 'bike_busy', message: 'm' } });
    const r = await api.POST('/api/bike/connect', { body: { address: 'x' } as never });
    expect(() => unwrap(r)).toThrow(ApiError);
    try {
      unwrap(r);
    } catch (e) {
      expect((e as ApiError).status).toBe(409);
      expect((e as ApiError).code).toBe('bike_busy');
      expect((e as ApiError).message).toBe('m');
    }
  });

  it('returns data on success', async () => {
    stubFetch(200, [{ id: 1 }]);
    const r = await api.GET('/api/riders');
    expect(unwrap(r)).toEqual([{ id: 1 }]);
  });
});
