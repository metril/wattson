import { createServer } from 'node:http';
import { once } from 'node:events';
import { existsSync, readFileSync } from 'node:fs';
import type { AddressInfo } from 'node:net';
import { resolve } from 'node:path';

const DIR = resolve(import.meta.dirname, 'fixtures/mly');

export interface FakeMapillary {
  /** Base URL, e.g. http://127.0.0.1:41234 */
  url: string;
  /** Value for settings `mapillary.graph_url`. */
  graphUrl: string;
  /** Value for settings `mapillary.tiles_url`. */
  tilesUrl: string;
  /** Request paths received, newest last. */
  requests: string[];
  /** While true, photo downloads (`/img/*`) answer 502. */
  fail: boolean;
  /** JPEG served for every photo; the committed 64x48 fixture when null. */
  image: Buffer | null;
  close(): Promise<void>;
}

/** Serves the committed fixtures (see internal/mapillary/testdata/genfixture) like Mapillary's tiles, Graph API and CDN. */
export async function startFakeMapillary(): Promise<FakeMapillary> {
  const jpeg = readFileSync(resolve(DIR, 'img.jpg'));
  const requests: string[] = [];
  let self = '';
  const fake = { requests, fail: false, image: null } as FakeMapillary;

  const srv = createServer((req, res) => {
    const path = new URL(req.url ?? '/', 'http://x').pathname;
    requests.push(path);
    const json = (status: number, body: unknown) => {
      res.writeHead(status, { 'content-type': 'application/json' });
      res.end(JSON.stringify(body));
    };
    let m: RegExpExecArray | null;
    if ((m = /^\/tiles\/(\d+)\/(\d+)\/(\d+)$/.exec(path))) {
      const file = resolve(DIR, m[1], m[2], m[3]);
      if (!existsSync(file)) {
        res.writeHead(404).end();
        return;
      }
      res.writeHead(200, { 'content-type': 'application/vnd.mapbox-vector-tile' });
      res.end(readFileSync(file));
    } else if (path === '/graph/images') {
      json(200, { data: [] });
    } else if ((m = /^\/graph\/(\d+)$/.exec(path))) {
      json(200, { thumb_2048_url: `${self}/img/${m[1]}.jpg`, creator: { username: 'e2e' }, id: m[1] });
    } else if (path.startsWith('/img/')) {
      if (fake.fail) {
        res.writeHead(502).end();
        return;
      }
      const body = fake.image ?? jpeg;
      res.writeHead(200, { 'content-type': 'image/jpeg', 'content-length': body.length });
      res.end(body);
    } else {
      res.writeHead(404).end();
    }
  });
  srv.listen(0, '127.0.0.1');
  await once(srv, 'listening');
  self = `http://127.0.0.1:${(srv.address() as AddressInfo).port}`;
  Object.assign(fake, {
    url: self,
    graphUrl: `${self}/graph`,
    tilesUrl: `${self}/tiles`,
    async close() {
      srv.closeAllConnections();
      srv.close();
      await once(srv, 'close');
    },
  });
  return fake;
}
