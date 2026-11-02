import { createServer, type Server } from 'node:http';
import { once } from 'node:events';
import type { AddressInfo } from 'node:net';

export interface FakeBRouter {
  /** Base URL, e.g. http://127.0.0.1:41234 */
  url: string;
  /** Every /brouter query string received, decoded. */
  queries: URLSearchParams[];
  close(): Promise<void>;
}

const R = 6_371_000;
const rad = (d: number) => (d * Math.PI) / 180;

function haversine(a: [number, number], b: [number, number]): number {
  const dLat = rad(b[1] - a[1]);
  const dLon = rad(b[0] - a[0]);
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(rad(a[1])) * Math.cos(rad(b[1])) * Math.sin(dLon / 2) ** 2;
  return 2 * R * Math.asin(Math.sqrt(h));
}

/** Elevation: flat 100 m for the first 60 m, then a steady 6 % climb. */
export const eleAt = (d: number) => (d <= 60 ? 100 : 100 + (d - 60) * 0.06);

/** A straight line through the waypoints with a point every 10 m ([lon, lat, ele]). */
export function line(wps: [number, number][]): [number, number, number][] {
  const out: [number, number, number][] = [];
  let base = 0;
  for (let i = 0; i + 1 < wps.length; i++) {
    const a = wps[i];
    const b = wps[i + 1];
    const len = haversine(a, b);
    const n = Math.max(1, Math.round(len / 10));
    for (let k = i === 0 ? 0 : 1; k <= n; k++) {
      const f = k / n;
      out.push([a[0] + (b[0] - a[0]) * f, a[1] + (b[1] - a[1]) * f, eleAt(base + len * f)]);
    }
    base += len;
  }
  return out;
}

/** An in-process BRouter stand-in: no tiles, no network, one per worker. */
export async function startFakeBRouter(): Promise<FakeBRouter> {
  const queries: URLSearchParams[] = [];
  const server: Server = createServer((req, res) => {
    const u = new URL(req.url ?? '/', 'http://x');
    if (u.pathname === '/brouter') {
      queries.push(u.searchParams);
      const wps = (u.searchParams.get('lonlats') ?? '')
        .split('|')
        .map((p) => p.split(',').map(Number) as [number, number])
        .filter((p) => p.length === 2 && p.every(Number.isFinite));
      if (wps.length < 2) {
        res.writeHead(400).end('bad lonlats');
        return;
      }
      const body = {
        type: 'FeatureCollection',
        features: [{ type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates: line(wps) } }],
      };
      res.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify(body));
      return;
    }
    res.writeHead(200, { 'content-type': 'text/plain' }).end('ok');
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const { port } = server.address() as AddressInfo;
  return {
    url: `http://127.0.0.1:${port}`,
    queries,
    async close() {
      server.closeAllConnections();
      server.close();
      await once(server, 'close');
    },
  };
}
