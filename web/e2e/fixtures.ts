import { test as base, expect } from '@playwright/test';
import { spawn, type ChildProcess } from 'node:child_process';
import { once } from 'node:events';
import { mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

import { startFakeBRouter, type FakeBRouter } from './fake-brouter';
import { ADMIN } from './helpers';

const BINARY = resolve(import.meta.dirname, '../../wattson');

/** A `wattson serve` owned by one worker: its own data dir and port. */
export interface Server {
  url: string;
  /** Stops the server, empties its data dir and starts it again (a first-run server). */
  fresh(): Promise<string>;
  /** Everything the server logged so far. */
  log(): string;
}

async function freePort(): Promise<number> {
  const s = createServer();
  s.listen(0, '127.0.0.1');
  await once(s, 'listening');
  const { port } = s.address() as { port: number };
  s.close();
  await once(s, 'close');
  return port;
}

async function waitHealthy(url: string, proc: ChildProcess, getLog: () => string) {
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    if (proc.exitCode !== null) throw new Error(`wattson exited with ${proc.exitCode}\n${getLog()}`);
    try {
      if ((await fetch(`${url}/healthz`)).ok) return;
    } catch {
      /* not listening yet */
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error(`wattson did not become healthy\n${getLog()}`);
}

export type Cookie = { name: string; value: string; url: string };

/** Creates the first admin through the API; returns the session cookies for `context.addCookies`. */
export async function setupAdmin(baseURL: string): Promise<Cookie[]> {
  const res = await fetch(`${baseURL}/api/setup`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(ADMIN),
  });
  if (res.status !== 201) throw new Error(`setup failed: ${res.status} ${await res.text()}`);
  return res.headers.getSetCookie().map((c) => {
    const [pair] = c.split(';');
    const i = pair.indexOf('=');
    return { name: pair.slice(0, i), value: pair.slice(i + 1), url: baseURL };
  });
}

/** Sends a signed-in API request from Node (no Origin header, so no CSRF check). */
export async function apiAs(baseURL: string, cookies: Cookie[], method: string, path: string, body?: unknown) {
  const res = await fetch(`${baseURL}${path}`, {
    method,
    headers: { 'content-type': 'application/json', cookie: cookies.map((c) => `${c.name}=${c.value}`).join('; ') },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`);
  return res;
}

export const test = base.extend<{ baseURL: string }, { server: Server; fakeBRouter: FakeBRouter; segmentsDir: string }>({
  // One fake BRouter per worker, in this process.
  fakeBRouter: [
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      const b = await startFakeBRouter();
      await use(b);
      await b.close();
    },
    { scope: 'worker' },
  ],

  // A map-regions directory holding one empty tile, E5_N45 (7.5E 45.5N), so the missing-tile check passes.
  segmentsDir: [
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      const dir = mkdtempSync(join(tmpdir(), 'wattson-e2e-seg-'));
      writeFileSync(join(dir, 'E5_N45.rd5'), '');
      await use(dir);
      rmSync(dir, { recursive: true, force: true });
    },
    { scope: 'worker' },
  ],

  server: [
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      const dir = mkdtempSync(join(tmpdir(), 'wattson-e2e-'));
      const port = await freePort();
      const url = `http://127.0.0.1:${port}`;
      let proc: ChildProcess | undefined;
      let out = '';

      const stop = async () => {
        if (!proc || proc.exitCode !== null) return;
        proc.kill('SIGTERM');
        await once(proc, 'exit');
      };
      const start = async () => {
        const env = { ...process.env };
        delete env.WATTSON_DATA_DIR;
        delete env.WATTSON_LISTEN;
        const p = spawn(BINARY, ['serve', '--data-dir', dir, '--listen', `127.0.0.1:${port}`], { env });
        proc = p;
        p.stdout.on('data', (d) => (out += d));
        p.stderr.on('data', (d) => (out += d));
        await waitHealthy(url, p, () => out);
      };

      await start();
      await use({
        url,
        log: () => out,
        async fresh() {
          await stop();
          for (const f of readdirSync(dir)) rmSync(join(dir, f), { recursive: true, force: true });
          await start();
          return url;
        },
      });
      await stop();
      rmSync(dir, { recursive: true, force: true });
    },
    { scope: 'worker' },
  ],

  // Every test gets a first-run server (empty data dir) on the worker's port.
  baseURL: async ({ server }, use) => {
    await use(await server.fresh());
  },
});

export { expect };
