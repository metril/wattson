// Hand-written service worker: cache-first for map tiles only. The app shell,
// the API and everything else go straight to the network, untouched.
import { createCore } from './core';
import { isTileRequest } from './corridor';

declare const self: ServiceWorkerGlobalScope;

const core = createCore({
  caches: self.caches,
  fetch: (url, init) => self.fetch(url, init),
  estimate: () => self.navigator.storage?.estimate?.(),
});

self.addEventListener('install', () => {
  void self.skipWaiting();
});

self.addEventListener('activate', (e) => {
  e.waitUntil(core.activate().then(() => self.clients.claim()));
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET' || !isTileRequest(req.url)) return; // not ours: the browser handles it
  e.respondWith(core.handleFetch(req.url, (p) => e.waitUntil(p)));
});

self.addEventListener('message', (e) => {
  const d = e.data as { type?: string; id?: unknown; urls?: unknown } | null;
  if (d?.type === 'cancel' && typeof d.id === 'string') {
    core.cancel(d.id);
    return;
  }
  const port = e.ports[0];
  if (d?.type !== 'prefetch' || typeof d.id !== 'string' || !Array.isArray(d.urls) || !port) return;
  const urls = d.urls;
  e.waitUntil(core.prefetch(d.id, urls, port).catch(() => port.postMessage({ type: 'done', cached: 0, failed: urls.length, total: urls.length })));
});
