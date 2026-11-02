import { test as base, expect } from '@playwright/test';
import { spawn, type ChildProcess } from 'node:child_process';
import { once } from 'node:events';
import { mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

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

export const test = base.extend<{ baseURL: string }, { server: Server }>({
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
