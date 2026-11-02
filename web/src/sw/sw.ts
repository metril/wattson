// Hand-written service worker: cache-first for map tiles only. The app shell,
// the API and everything else go straight to the network, untouched.
import { CACHE, cacheKey, evictCount, isTileRequest } from './corridor';

declare const self: ServiceWorkerGlobalScope;

const MIN_FREE_BYTES = 100 * 1024 * 1024;
const CONCURRENCY = 6;
const PROGRESS_EVERY = 20;

self.addEventListener('install', () => {
  void self.skipWaiting();
});

self.addEventListener('activate', (e) => {
  e.waitUntil(
    (async () => {
      for (const name of await caches.keys()) {
        if (name.startsWith('wattson-tiles-') && name !== CACHE) await caches.delete(name);
      }
      await self.clients.claim();
    })(),
  );
});

// Evictions and writes run one at a time so the entry count stays honest.
let lock: Promise<unknown> = Promise.resolve();
let count = -1;

function store(key: string, res: Response): Promise<void> {
  const run = async () => {
    try {
      const cache = await caches.open(CACHE);
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
    } catch {
      count = -1; // quota or a racing delete: recount next time
    }
  };
  const next = lock.then(run, run);
  lock = next;
  return next;
}

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET' || !isTileRequest(req.url)) return; // not ours: the browser handles it
  e.respondWith(
    (async () => {
      const key = cacheKey(req.url);
      const cache = await caches.open(CACHE);
      const hit = await cache.match(key, { ignoreVary: true });
      if (hit) return hit;
      const res = await fetch(req.url, { mode: 'cors' });
      if (res.ok) e.waitUntil(store(key, res.clone()));
      return res;
    })(),
  );
});

async function lowOnSpace(): Promise<boolean> {
  try {
    const est = await navigator.storage?.estimate?.();
    return !!est && est.quota !== undefined && est.usage !== undefined && est.quota - est.usage < MIN_FREE_BYTES;
  } catch {
    return false;
  }
}

async function prefetch(urls: string[], port: MessagePort): Promise<void> {
  if (await lowOnSpace()) {
    port.postMessage({ error: 'quota' });
    return;
  }
  const cache = await caches.open(CACHE);
  const total = urls.length;
  let next = 0;
  let done = 0;
  let cached = 0;
  let failed = 0;
  const one = async (url: string) => {
    const key = cacheKey(url);
    try {
      if (await cache.match(key, { ignoreVary: true })) {
        cached++;
        return;
      }
      const res = await fetch(url, { mode: 'cors' });
      if (!res.ok) throw new Error(String(res.status));
      await store(key, res);
      cached++;
    } catch {
      failed++;
    }
  };
  const worker = async () => {
    while (next < total) {
      await one(urls[next++]);
      done++;
      if (done % PROGRESS_EVERY === 0 && done < total) port.postMessage({ type: 'progress', done, total });
    }
  };
  await Promise.all(Array.from({ length: Math.min(CONCURRENCY, total) }, worker));
  port.postMessage({ type: 'progress', done: total, total });
  port.postMessage({ type: 'done', cached, failed });
}

self.addEventListener('message', (e) => {
  const d = e.data as { type?: string; urls?: unknown } | null;
  const port = e.ports[0];
  if (d?.type !== 'prefetch' || !Array.isArray(d.urls) || !port) return;
  // Only tile urls: the page cannot make the worker fetch anything else.
  const urls = [...new Set(d.urls.filter((u): u is string => typeof u === 'string' && isTileRequest(u)))];
  e.waitUntil(prefetch(urls, port).catch(() => port.postMessage({ type: 'done', cached: 0, failed: urls.length })));
});
