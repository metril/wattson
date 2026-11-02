import { createCore, type Env } from './core';
import { CACHE, MAX_ENTRIES } from './corridor';

const T = (n: number) => `https://s3.amazonaws.com/elevation-tiles-prod/terrarium/12/${n}/5.png`;

class FakeCache {
  m = new Map<string, Response>();
  putError: Error | null = null;
  async match(k: string) {
    const r = this.m.get(k);
    return r ? r.clone() : undefined;
  }
  async put(k: string, r: Response) {
    if (this.putError) throw this.putError;
    this.m.delete(k);
    this.m.set(k, r);
  }
  async delete(k: string) {
    return this.m.delete(k);
  }
  async keys() {
    return [...this.m.keys()] as unknown as Request[];
  }
}
class FakeCaches {
  all = new Map<string, FakeCache>();
  broken = false;
  async open(n: string) {
    if (this.broken) throw new Error('boom');
    if (!this.all.has(n)) this.all.set(n, new FakeCache());
    return this.all.get(n)!;
  }
  async keys() {
    return [...this.all.keys()];
  }
  async delete(n: string) {
    return this.all.delete(n);
  }
  get tiles() {
    return this.all.get(CACHE)!;
  }
}

function setup(over: Partial<Env> = {}) {
  const caches = new FakeCaches();
  const net = vi.fn(async (u: string) => new Response(`png:${u}`, { status: 200 }));
  const pending: Promise<unknown>[] = [];
  const core = createCore({ caches: caches as unknown as CacheStorage, fetch: net, ...over });
  const wait = (p: Promise<unknown>) => void pending.push(p);
  const settle = () => Promise.all(pending.splice(0));
  return { caches, net, core, wait, settle };
}
const port = () => {
  const msgs: Record<string, unknown>[] = [];
  return { msgs, p: { postMessage: (m: Record<string, unknown>) => void msgs.push(m) } as unknown as MessagePort };
};

describe('sw core: fetch', () => {
  it('goes to the network once, then serves from the cache', async () => {
    const { core, net, wait, settle } = setup();
    expect(await (await core.handleFetch(`${T(1)}?v=1`, wait)).text()).toContain('png');
    await settle();
    expect(await (await core.handleFetch(T(1), wait)).text()).toContain('png');
    expect(net).toHaveBeenCalledTimes(1);
  });

  it('does not store error responses', async () => {
    const { core, caches, net, wait, settle } = setup();
    net.mockResolvedValue(new Response('no', { status: 502 }));
    expect((await core.handleFetch(T(1), wait)).status).toBe(502);
    await settle();
    expect(caches.tiles?.m.size ?? 0).toBe(0);
  });

  it('falls back to the network when the cache is unusable', async () => {
    const { core, caches, net, wait } = setup();
    caches.broken = true;
    expect((await core.handleFetch(T(1), wait)).status).toBe(200);
    expect(net).toHaveBeenCalledTimes(1);
  });

  it('concurrent misses of one tile store it once', async () => {
    const { core, caches, wait, settle } = setup();
    await Promise.all([core.handleFetch(T(1), wait), core.handleFetch(T(1), wait)]);
    await settle();
    expect(caches.tiles.m.size).toBe(1);
  });

  it('evicts the oldest tenth when full and keeps recently used tiles', async () => {
    const { core, caches, wait, settle } = setup();
    const cache = await caches.open(CACHE);
    for (let i = 0; i < MAX_ENTRIES; i++) cache.m.set(`https://s3.amazonaws.com/elevation-tiles-prod/terrarium/9/${i}/0.png`, new Response('x'));
    const used = 'https://s3.amazonaws.com/elevation-tiles-prod/terrarium/9/0/0.png';
    await core.handleFetch(used, wait); // a hit moves it to the back
    await settle();
    await core.handleFetch(T(99999), wait); // a miss over capacity
    await settle();
    expect(cache.m.size).toBe(MAX_ENTRIES - 400 + 1);
    expect(cache.m.has(used)).toBe(true);
    expect(cache.m.has('https://s3.amazonaws.com/elevation-tiles-prod/terrarium/9/1/0.png')).toBe(false);
    expect(cache.m.has(T(99999).split('?')[0])).toBe(true);
  });
});

describe('sw core: lifecycle', () => {
  it('activate drops other wattson-tiles caches only', async () => {
    const { core, caches } = setup();
    await caches.open('wattson-tiles-v0');
    await caches.open('other');
    await caches.open(CACHE);
    await core.activate();
    expect(await caches.keys()).toEqual(['other', CACHE]);
  });
});

describe('sw core: prefetch', () => {
  it('fetches, skips present tiles, reports progress and totals', async () => {
    const { core, caches, net } = setup();
    const c = await caches.open(CACHE);
    c.m.set(T(0), new Response('x'));
    const { p, msgs } = port();
    const urls = Array.from({ length: 45 }, (_, i) => T(i));
    await core.prefetch('a', urls, p);
    expect(net).toHaveBeenCalledTimes(44);
    expect(msgs.filter((m) => m.type === 'progress').map((m) => m.done)).toEqual([20, 40, 45]);
    expect(msgs.at(-1)).toEqual({ type: 'done', cached: 45, failed: 0, total: 45 });
    expect(c.m.size).toBe(45);
  });

  it('only fetches tile urls', async () => {
    const { core, net } = setup();
    const { p, msgs } = port();
    await core.prefetch('a', [T(1), 'https://evil.example/x/1/2/3.png', '/api/routes', 5, T(1)], p);
    expect(net).toHaveBeenCalledTimes(1);
    expect(msgs.at(-1)).toMatchObject({ type: 'done', cached: 1, total: 1 });
  });

  it('counts failed fetches and failed cache writes as failed, not cached', async () => {
    const { core, caches, net } = setup();
    net.mockImplementation(async (u: string) => (u === T(1) ? new Response('', { status: 503 }) : new Response('ok')));
    const c = await caches.open(CACHE);
    const orig = c.put.bind(c);
    c.put = async (k, r) => {
      if (k === T(2)) throw new Error('disk');
      return orig(k, r);
    };
    const { p, msgs } = port();
    await core.prefetch('a', [T(1), T(2), T(3)], p);
    expect(msgs.at(-1)).toEqual({ type: 'done', cached: 1, failed: 2, total: 3 });
  });

  it('a full disk stops the job with a quota error', async () => {
    const { core, caches } = setup();
    const c = await caches.open(CACHE);
    c.putError = Object.assign(new Error('full'), { name: 'QuotaExceededError' });
    const { p, msgs } = port();
    await core.prefetch('a', Array.from({ length: 30 }, (_, i) => T(i)), p);
    expect(msgs.at(-1)).toEqual({ error: 'quota' });
    expect(msgs.some((m) => m.type === 'done')).toBe(false);
  });

  it('refuses with quota when less than 100 MB is free', async () => {
    const { core, net } = setup({ estimate: async () => ({ quota: 150e6, usage: 100e6 }) });
    const { p, msgs } = port();
    await core.prefetch('a', [T(1)], p);
    expect(msgs).toEqual([{ error: 'quota' }]);
    expect(net).not.toHaveBeenCalled();
  });

  it('never has more than 6 fetches in flight, and a new job cancels the old', async () => {
    let inflight = 0;
    let peak = 0;
    const gates: (() => void)[] = [];
    const { core, net } = setup();
    net.mockImplementation(
      (u: string, init?: RequestInit) =>
        new Promise<Response>((resolve, reject) => {
          inflight++;
          peak = Math.max(peak, inflight);
          init?.signal?.addEventListener('abort', () => {
            inflight--;
            reject(new Error('aborted'));
          });
          gates.push(() => {
            inflight--;
            resolve(new Response(u));
          });
        }),
    );
    const a = port();
    const jobA = core.prefetch('a', Array.from({ length: 50 }, (_, i) => T(i)), a.p);
    await vi.waitFor(() => expect(gates.length).toBe(6));
    const b = port();
    const jobB = core.prefetch('b', Array.from({ length: 10 }, (_, i) => T(100 + i)), b.p);
    await jobA;
    expect(a.msgs).toContainEqual({ type: 'cancelled' });
    expect(a.msgs.some((m) => m.type === 'done')).toBe(false);
    for (let i = 0; i < 100 && !b.msgs.some((m) => m.type === 'done'); i++) {
      gates.splice(0).forEach((g) => g());
      await new Promise((r) => setTimeout(r, 0));
    }
    await jobB;
    expect(peak).toBeLessThanOrEqual(6);
    expect(b.msgs.at(-1)).toMatchObject({ type: 'done', total: 10 });
  });

  it('cancel by id stops the matching job only', async () => {
    const { core, net } = setup();
    net.mockImplementation((_u: string, init?: RequestInit) => new Promise<Response>((_r, reject) => init?.signal?.addEventListener('abort', () => reject(new Error('x')))));
    const a = port();
    const job = core.prefetch('a', [T(1), T(2)], a.p);
    await vi.waitFor(() => expect(net).toHaveBeenCalled());
    core.cancel('other');
    expect(a.msgs).toEqual([]);
    core.cancel('a');
    await job;
    expect(a.msgs).toEqual([{ type: 'cancelled' }]);
  });
});
