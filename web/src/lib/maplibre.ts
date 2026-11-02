import workerUrl from 'maplibre-gl/dist/maplibre-gl-csp-worker.js?url';

export type MapLibre = typeof import('maplibre-gl');

/**
 * Loads MapLibre and its stylesheet on demand. The CSP build runs its worker
 * from a same-origin file (workerUrl) instead of a blob: URL, so the page
 * needs no blob: in worker-src, and the service worker always controls the
 * worker's tile fetches.
 */
export async function loadMapLibre(): Promise<MapLibre> {
  const [mod] = await Promise.all([import('maplibre-gl/dist/maplibre-gl-csp'), import('maplibre-gl/dist/maplibre-gl.css')]);
  // The UMD build may arrive as a bare default export, depending on the bundler's interop.
  const ns = mod as unknown as MapLibre & { default: MapLibre };
  const ml = ns.Map ? ns : ns.default;
  ml.setWorkerUrl(workerUrl);
  return ml;
}
