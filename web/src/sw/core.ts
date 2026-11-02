// The service worker's logic with its environment injected, so sw.ts stays a
// thin wiring layer and this file can be unit-tested with a fake cache.
import { CACHE, cacheKey, evictCount, isTileRequest } from './corridor';

const MIN_FREE_BYTES = 100 * 1024 * 1024;
const CONCURRENCY = 6;
const PROGRESS_EVERY = 20;

export interface Env {
  caches: CacheStorage;
  fetch: (url: string, init?: RequestInit) => Promise<Response>;
  estimate?: () => Promise<{ quota?: number; usage?: number } | undefined>;
}

type Stored = 'ok' | 'quota' | 'fail';
type Job = { id: string; cancelled: boolean; quota: boolean; ctl: AbortController; port: MessagePort };

export function createCore(env: Env) {
  // Cache writes and evictions run one at a time so the entry count stays honest.
  let lock: Promise<unknown> = Promise.resolve();
  let count = -1; // entries in the cache, -1 = unknown
  const touched = new Set<string>();
  let job: Job | null = null;

  const serial = <T>(fn: () => Promise<T>): Promise<T> => {
    const next = lock.then(fn, fn);
    lock = next.then(
      () => undefined,
      () => undefined,
    );
    return next;
  };

  const fail = (e: unknown): Stored => {
    count = -1; // quota or a racing delete: recount next time
    return e instanceof Error && e.name === 'QuotaExceededError' ? 'quota' : 'fail';
  };

  /** Stores a response, evicting the oldest entries first. Reports whether it really is in the cache. */
  function store(key: string, res: Response): Promise<Stored> {
    return serial(async () => {
      try {
        const cache = await env.caches.open(CACHE);
        if (await cache.match(key, { ignoreVary: true })) return 'ok';
        if (count < 0) count = (await cache.keys()).length;
        const drop = evictCount(count, 1);
        if (drop > 0) {
          const keys = await cache.keys(); // oldest first
          const old = keys.slice(0, drop);
          await Promise.all(old.map((k) => cache.delete(k)));
          count = keys.length - old.length;
        }
        await cache.put(key, res);
        count++;
        return 'ok';
      } catch (e) {
        return fail(e);
      }
    });
  }

  /** Move a used entry to the back of the eviction queue (once per key per worker lifetime). */
  function touch(key: string, res: Response): Promise<void> {
    return serial(async () => {
      try {
        const cache = await env.caches.open(CACHE);
        await cache.delete(key);
        await cache.put(key, res);
      } catch (e) {
        fail(e);
      }
    });
  }

  async function handleFetch(url: string, waitUntil: (p: Promise<unknown>) => void): Promise<Response> {
    const key = cacheKey(url);
    try {
      const cache = await env.caches.open(CACHE);
      const hit = await cache.match(key, { ignoreVary: true });
      if (hit) {
        if (!touched.has(key)) {
          touched.add(key);
          waitUntil(touch(key, hit.clone()));
        }
        return hit;
      }
    } catch {
      // the cache is unusable: a tile that loads online must still load
    }
    const res = await env.fetch(url, { mode: 'cors' });
    if (res.ok) waitUntil(store(key, res.clone()));
    return res;
  }

  async function activate(): Promise<void> {
    for (const name of await env.caches.keys()) {
      if (name.startsWith('wattson-tiles-') && name !== CACHE) await env.caches.delete(name);
    }
  }

  async function lowOnSpace(): Promise<boolean> {
    try {
      const est = await env.estimate?.();
      return !!est && est.quota !== undefined && est.usage !== undefined && est.quota - est.usage < MIN_FREE_BYTES;
    } catch {
      return false;
    }
  }

  function stop(j: Job) {
    j.cancelled = true;
    j.ctl.abort();
  }

  /** Cancel the active job (or only the one named `id`). */
  function cancel(id?: string): void {
    if (!job || (id !== undefined && job.id !== id)) return;
    const j = job;
    stop(j);
    job = null;
    try {
      j.port.postMessage({ type: 'cancelled' });
    } catch {
      /* the page is gone */
    }
  }

  /** One job at a time: a new one cancels the old, so at most CONCURRENCY tile fetches are in flight. */
  async function prefetch(id: string, raw: unknown[], port: MessagePort): Promise<void> {
    cancel();
    const j: Job = { id, cancelled: false, quota: false, ctl: new AbortController(), port };
    job = j;
    // Only tile urls: the page cannot make the worker fetch anything else.
    const urls = [...new Set(raw.filter((u): u is string => typeof u === 'string' && isTileRequest(u)))];
    try {
      if (await lowOnSpace()) {
        port.postMessage({ error: 'quota' });
        return;
      }
      if (j.cancelled) return;
      const total = urls.length;
      let next = 0;
      let done = 0;
      let cached = 0;
      let failed = 0;
      const one = async (url: string) => {
        const key = cacheKey(url);
        try {
          const cache = await env.caches.open(CACHE);
          if (await cache.match(key, { ignoreVary: true })) {
            cached++;
            return;
          }
          const res = await env.fetch(url, { mode: 'cors', signal: j.ctl.signal });
          if (!res.ok) {
            failed++;
            return;
          }
          const s = await store(key, res);
          if (s === 'ok') cached++;
          else {
            failed++;
            if (s === 'quota') {
              j.quota = true;
              stop(j);
            }
          }
        } catch {
          failed++;
        }
      };
      const worker = async () => {
        while (!j.cancelled && next < total) {
          await one(urls[next++]);
          done++;
          if (!j.cancelled && done % PROGRESS_EVERY === 0 && done < total) port.postMessage({ type: 'progress', done, total });
        }
      };
      await Promise.all(Array.from({ length: Math.min(CONCURRENCY, total) }, worker));
      if (j.quota) port.postMessage({ error: 'quota' });
      else if (!j.cancelled) {
        port.postMessage({ type: 'progress', done: total, total });
        port.postMessage({ type: 'done', cached, failed, total });
      }
    } finally {
      if (job === j) job = null;
    }
  }

  return { handleFetch, activate, prefetch, cancel };
}
