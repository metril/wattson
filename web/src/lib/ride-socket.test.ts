import { RideSocket } from './ride-socket';
import { ApiError } from './api';
import { bikeFixture, FakeWebSocket, tickFixture } from '@/test-ws';

const mk = (over: Partial<ConstructorParameters<typeof RideSocket>[0]> = {}) => {
  const onUnauthorized = vi.fn();
  const s = new RideSocket({
    url: 'ws://x/ws',
    onUnauthorized,
    WebSocketImpl: FakeWebSocket as unknown as typeof WebSocket,
    ...over,
  });
  return { s, onUnauthorized };
};
const stateMsg = (over = {}) => ({ type: 'state', bike: bikeFixture(), tick: tickFixture(over) });

const meStatus = (status: number) => vi.fn(async () => new Response(null, { status }));

beforeEach(() => {
  vi.useFakeTimers();
  FakeWebSocket.reset();
  vi.stubGlobal('fetch', meStatus(200));
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('RideSocket', () => {
  it('sends wattson.v1 and bearer protocol', () => {
    mk({ token: 'wat_k' }).s.start();
    expect(FakeWebSocket.last.protocols).toEqual(['wattson.v1', 'bearer.wat_k']);
  });

  it('offers only wattson.v1 without a token', () => {
    mk().s.start();
    expect(FakeWebSocket.last.protocols).toEqual(['wattson.v1']);
  });

  it('send resolves on ack', async () => {
    const { s } = mk();
    s.start();
    const ws = FakeWebSocket.last;
    ws.open();
    ws.receive(stateMsg());
    const p = s.send('start');
    expect(ws.sent[0]).toBe('{"type":"command","id":"c1","command":"start","value":0}');
    ws.receive({ type: 'ack', id: 'c1' });
    await expect(p).resolves.toBeUndefined();
  });

  it('send includes start options only when given', () => {
    const { s } = mk();
    s.start();
    const ws = FakeWebSocket.last;
    ws.open();
    ws.receive(stateMsg());
    void s.send('start', 0, { route_id: 3 }).catch(() => {});
    void s.send('pause').catch(() => {});
    expect(ws.sent[0]).toBe('{"type":"command","id":"c1","command":"start","value":0,"route_id":3}');
    expect(JSON.parse(ws.sent[1])).not.toHaveProperty('route_id');
  });

  it('send writes workout_id and target_power_w after value, and nothing else', () => {
    const { s } = mk();
    s.start();
    const ws = FakeWebSocket.last;
    ws.open();
    ws.receive(stateMsg());
    void s.send('start', 0, { workout_id: 5 }).catch(() => {});
    void s.send('start', 0, { target_power_w: 180 }).catch(() => {});
    void s.send('set_target_power', 250).catch(() => {});
    expect(ws.sent[0]).toBe('{"type":"command","id":"c1","command":"start","value":0,"workout_id":5}');
    expect(ws.sent[1]).toBe('{"type":"command","id":"c2","command":"start","value":0,"target_power_w":180}');
    expect(ws.sent[2]).toBe('{"type":"command","id":"c3","command":"set_target_power","value":250}');
  });

  it('error rejects with code', async () => {
    const { s } = mk();
    s.start();
    const ws = FakeWebSocket.last;
    ws.open();
    const p = s.send('start');
    ws.receive({ type: 'error', id: 'c1', error: { code: 'no_bike', message: 'no bike' } });
    const err = await p.catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.code).toBe('no_bike');
  });

  it('rejects with disconnected on close and while down', async () => {
    const { s } = mk();
    s.start();
    const ws = FakeWebSocket.last;
    ws.open();
    const p = s.send('pause');
    ws.drop(1006);
    await expect(p).rejects.toMatchObject({ code: 'disconnected' });
    await expect(s.send('pause')).rejects.toMatchObject({ code: 'disconnected' });
  });

  it('times out after 5 s', async () => {
    const { s } = mk();
    s.start();
    FakeWebSocket.last.open();
    const p = s.send('stop');
    const assertion = expect(p).rejects.toMatchObject({ code: 'timeout' });
    await vi.advanceTimersByTimeAsync(5000);
    await assertion;
  });

  it('reconnects with backoff and rehydrates from state', () => {
    const { s } = mk();
    const seen: boolean[] = [];
    s.onStatus((st) => seen.push(st.connected));
    s.start();
    expect(FakeWebSocket.instances).toHaveLength(1);
    FakeWebSocket.last.drop(1006);
    vi.advanceTimersByTime(999);
    expect(FakeWebSocket.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeWebSocket.instances).toHaveLength(2);
    FakeWebSocket.last.drop(1006);
    vi.advanceTimersByTime(2000);
    expect(FakeWebSocket.instances).toHaveLength(3);
    FakeWebSocket.last.drop(1006);
    vi.advanceTimersByTime(3999);
    expect(FakeWebSocket.instances).toHaveLength(3);
    vi.advanceTimersByTime(1);
    expect(FakeWebSocket.instances).toHaveLength(4);

    FakeWebSocket.last.open();
    FakeWebSocket.last.receive(stateMsg({ power_w: 150, state: 'riding' }));
    expect(s.ticks.latest?.power_w).toBe(150);
    expect(seen.at(-1)).toBe(true);
    expect(s.getStatus()).toMatchObject({ connected: true, state: 'riding' });

    // a state message resets the backoff to 1 s
    FakeWebSocket.last.drop(1006);
    vi.advanceTimersByTime(1000);
    expect(FakeWebSocket.instances).toHaveLength(5);
  });

  it('caps the backoff at 10 s', () => {
    const { s } = mk();
    s.start();
    for (const wait of [1, 2, 4, 8, 10, 10, 10]) {
      const n = FakeWebSocket.instances.length;
      FakeWebSocket.last.drop(1006);
      vi.advanceTimersByTime(wait * 1000 - 1);
      expect(FakeWebSocket.instances).toHaveLength(n);
      vi.advanceTimersByTime(1);
      expect(FakeWebSocket.instances).toHaveLength(n + 1);
    }
  });

  it('4401 close triggers onUnauthorized and no reconnect', () => {
    const { s, onUnauthorized } = mk();
    s.start();
    FakeWebSocket.last.drop(4401);
    vi.advanceTimersByTime(30_000);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(FakeWebSocket.instances).toHaveLength(1);
  });

  it('close() stops reconnecting', () => {
    const { s } = mk();
    s.start();
    s.close();
    vi.advanceTimersByTime(30_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
  });

  it('onStatus fires only on change', () => {
    const { s } = mk();
    const fn = vi.fn();
    s.onStatus(fn);
    s.start();
    const ws = FakeWebSocket.last;
    ws.open();
    ws.receive(stateMsg());
    expect(fn).toHaveBeenCalledTimes(1);
    for (let i = 0; i < 10; i++) ws.receive({ type: 'tick', tick: tickFixture({ power_w: i }) });
    expect(fn).toHaveBeenCalledTimes(1);
    expect(s.ticks.latest?.power_w).toBe(9);
    ws.receive({ type: 'tick', tick: tickFixture({ state: 'riding' }) });
    expect(fn).toHaveBeenCalledTimes(2);
    ws.receive({ type: 'tick', tick: tickFixture({ state: 'riding', alert: 'stale reading' }) });
    expect(fn).toHaveBeenCalledTimes(3);
    expect(fn.mock.calls[2][0].alert).toBe('stale reading');
    ws.receive({ type: 'bike', bike: bikeFixture({ last_error: 'boom' }) });
    expect(fn).toHaveBeenCalledTimes(4);
    ws.receive({ type: 'bike', bike: bikeFixture({ last_error: 'boom' }) });
    expect(fn).toHaveBeenCalledTimes(4);
  });

  it('a connecting bike wins over an idle tick', () => {
    const { s } = mk();
    s.start();
    const ws = FakeWebSocket.last;
    ws.open();
    ws.receive({ type: 'state', bike: bikeFixture({ state: 'idle', connected: false }), tick: tickFixture({ state: 'idle' }) });
    ws.receive({ type: 'bike', bike: bikeFixture({ state: 'connecting' }) });
    expect(s.getStatus().state).toBe('connecting');
  });

  describe('session probe', () => {
    it('after 2 closes without opening, a 401 from /api/auth/me stops reconnecting', async () => {
      const fetchMock = meStatus(401);
      vi.stubGlobal('fetch', fetchMock);
      const { s, onUnauthorized } = mk({ token: 'wat_k' });
      s.start();
      FakeWebSocket.last.drop(1006);
      await vi.advanceTimersByTimeAsync(1000);
      expect(fetchMock).not.toHaveBeenCalled();
      FakeWebSocket.last.drop(1006);
      await vi.advanceTimersByTimeAsync(0);
      expect(fetchMock).toHaveBeenCalledTimes(1);
      const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
      expect(url).toBe('/api/auth/me');
      expect(init.headers).toEqual({ Authorization: 'Bearer wat_k' });
      expect(onUnauthorized).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(30_000);
      expect(FakeWebSocket.instances).toHaveLength(2);
    });

    it('keeps reconnecting when the session is still valid', async () => {
      const { s, onUnauthorized } = mk();
      s.start();
      FakeWebSocket.last.drop(1006);
      await vi.advanceTimersByTimeAsync(1000);
      FakeWebSocket.last.drop(1006);
      await vi.advanceTimersByTimeAsync(2000);
      expect(onUnauthorized).not.toHaveBeenCalled();
      expect(FakeWebSocket.instances).toHaveLength(3);
    });

    it('does not probe when connections did open', async () => {
      const fetchMock = meStatus(401);
      vi.stubGlobal('fetch', fetchMock);
      const { s } = mk();
      s.start();
      for (let i = 0; i < 3; i++) {
        FakeWebSocket.last.open();
        FakeWebSocket.last.drop(1006);
        await vi.advanceTimersByTimeAsync(1000);
      }
      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('a network error while probing keeps retrying', async () => {
      vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('offline')));
      const { s, onUnauthorized } = mk();
      s.start();
      FakeWebSocket.last.drop(1006);
      await vi.advanceTimersByTimeAsync(1000);
      FakeWebSocket.last.drop(1006);
      await vi.advanceTimersByTimeAsync(2000);
      expect(onUnauthorized).not.toHaveBeenCalled();
      expect(FakeWebSocket.instances).toHaveLength(3);
    });
  });
});
