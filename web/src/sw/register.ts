/** Registers /sw.js. Production builds on secure origins only: dev servers must not cache. */
export function registerSW(): void {
  if (!import.meta.env.PROD || !('serviceWorker' in navigator) || !window.isSecureContext) return;
  const go = () => void navigator.serviceWorker.register('/sw.js').catch(() => {});
  if (document.readyState === 'complete') go();
  else window.addEventListener('load', go, { once: true });
}

export function hasController(): boolean {
  return 'serviceWorker' in navigator && !!navigator.serviceWorker.controller;
}

/** Calls fn when a worker takes control of the page (first visit, after install). */
export function onControllerChange(fn: () => void): () => void {
  if (!('serviceWorker' in navigator)) return () => {};
  navigator.serviceWorker.addEventListener('controllerchange', fn);
  return () => navigator.serviceWorker.removeEventListener('controllerchange', fn);
}

export type PrefetchResult = { cached: number; failed: number; total: number };

let jobs = 0;

/**
 * Ask the worker to fetch tiles into its cache. Rejects with Error("quota")
 * when the device is short on space and Error("cancelled") when `signal`
 * aborts (the worker then stops fetching) or another job replaces this one.
 */
export function prefetchRoute(
  urls: string[],
  onProgress: (done: number, total: number) => void,
  signal?: AbortSignal,
): Promise<PrefetchResult> {
  return new Promise((resolve, reject) => {
    const sw = 'serviceWorker' in navigator ? navigator.serviceWorker.controller : null;
    if (!sw) {
      reject(new Error('no_controller'));
      return;
    }
    const id = `${Date.now()}-${++jobs}`;
    const ch = new MessageChannel();
    const end = () => {
      ch.port1.close();
      signal?.removeEventListener('abort', onAbort);
    };
    const onAbort = () => {
      sw.postMessage({ type: 'cancel', id });
      end();
      reject(new Error('cancelled'));
    };
    if (signal?.aborted) {
      reject(new Error('cancelled'));
      return;
    }
    signal?.addEventListener('abort', onAbort);
    ch.port1.onmessage = (e: MessageEvent<{ type?: string; error?: string; done?: number; total?: number; cached?: number; failed?: number }>) => {
      const m = e.data;
      if (m.error) {
        end();
        reject(new Error(m.error));
      } else if (m.type === 'cancelled') {
        end();
        reject(new Error('cancelled'));
      } else if (m.type === 'progress') {
        onProgress(m.done ?? 0, m.total ?? 0);
      } else if (m.type === 'done') {
        end();
        resolve({ cached: m.cached ?? 0, failed: m.failed ?? 0, total: m.total ?? (m.cached ?? 0) + (m.failed ?? 0) });
      }
    };
    sw.postMessage({ type: 'prefetch', id, urls }, [ch.port2]);
  });
}
