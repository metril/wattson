import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { mainBundleBytes } from './check-size.mjs';

const script = join(dirname(fileURLToPath(import.meta.url)), 'check-size.mjs');

function fixture(jsBytes) {
  const dir = mkdtempSync(join(tmpdir(), 'size-'));
  mkdirSync(join(dir, 'assets'));
  writeFileSync(join(dir, 'assets/index-abc.js'), randomBytes(jsBytes));
  writeFileSync(join(dir, 'assets/index-abc.css'), 'body{margin:0}');
  writeFileSync(join(dir, 'assets/lazy-qr.js'), randomBytes(900_000));
  writeFileSync(
    join(dir, 'index.html'),
    '<!doctype html><html><head>' +
      '<script type="module" crossorigin src="/assets/index-abc.js"></script>' +
      '<link rel="stylesheet" crossorigin href="/assets/index-abc.css">' +
      '</head><body></body></html>',
  );
  return dir;
}

test('small bundle is under the limit and lazy chunks are ignored', () => {
  const dir = fixture(1024);
  try {
    const n = mainBundleBytes(dir);
    assert.ok(n > 1024 && n < 2048, `got ${n}`);
    const out = execFileSync('node', [script, dir], { encoding: 'utf8' });
    assert.match(out, /^main bundle \d+(\.\d)? KB gz\n$/);
  } finally {
    rmSync(dir, { recursive: true });
  }
});

test('600 KB of incompressible JS exceeds the limit', () => {
  const dir = fixture(600 * 1024);
  try {
    assert.ok(mainBundleBytes(dir) > 409600);
    const r = spawnSync('node', [script, dir], { encoding: 'utf8' });
    assert.equal(r.status, 1);
    assert.match(r.stderr, /main bundle \d+(\.\d)? KB gz exceeds 400 KB/);
  } finally {
    rmSync(dir, { recursive: true });
  }
});
