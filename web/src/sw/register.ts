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

/** Ask the worker to fetch tiles into its cache. Rejects with Error("quota") when the device is short on space. */
export function prefetchRoute(
  urls: string[],
  onProgress: (done: number, total: number) => void,
): Promise<{ cached: number; failed: number }> {
  return new Promise((resolve, reject) => {
    const sw = 'serviceWorker' in navigator ? navigator.serviceWorker.controller : null;
    if (!sw) {
      reject(new Error('no_controller'));
      return;
    }
    const ch = new MessageChannel();
    ch.port1.onmessage = (e: MessageEvent<{ type?: string; error?: string; done?: number; total?: number; cached?: number; failed?: number }>) => {
      const m = e.data;
      if (m.error) {
        ch.port1.close();
        reject(new Error(m.error));
      } else if (m.type === 'progress') {
        onProgress(m.done ?? 0, m.total ?? 0);
      } else if (m.type === 'done') {
        ch.port1.close();
        resolve({ cached: m.cached ?? 0, failed: m.failed ?? 0 });
      }
    };
    sw.postMessage({ type: 'prefetch', urls }, [ch.port2]);
  });
}
