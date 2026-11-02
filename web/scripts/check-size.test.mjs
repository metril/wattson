import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { chunkBytes, mainBundleBytes } from './check-size.mjs';

const script = join(dirname(fileURLToPath(import.meta.url)), 'check-size.mjs');

function fixture(jsBytes, chunks = { maplibre: 1024, ride3d: 1024, photo: 1024 }) {
  const dir = mkdtempSync(join(tmpdir(), 'size-'));
  mkdirSync(join(dir, 'assets'));
  writeFileSync(join(dir, 'assets/index-abc.js'), randomBytes(jsBytes));
  writeFileSync(join(dir, 'assets/index-abc.css'), 'body{margin:0}');
  writeFileSync(join(dir, 'assets/lazy-qr.js'), randomBytes(900_000));
  for (const [name, bytes] of Object.entries(chunks)) writeFileSync(join(dir, `assets/${name}-abc123.js`), randomBytes(bytes));
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
    assert.match(out, /^main bundle \d+(\.\d)? KB gz\n/);
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

// A dist whose only lazy chunk is `name` (plus the usual small entry).
function chunkFixture(name, bytes) {
  return fixture(1024, { maplibre: 1024, ride3d: 1024, photo: 1024, [name]: bytes });
}

test('chunkBytes gzips assets/<name>-*.js and is 0 when the chunk is missing', () => {
  const dir = chunkFixture('ride3d', 10 * 1024);
  try {
    const n = chunkBytes(dir, 'ride3d');
    assert.ok(n > 10 * 1024 && n < 11 * 1024, `got ${n}`);
    assert.equal(chunkBytes(dir, 'nothing'), 0);
  } finally {
    rmSync(dir, { recursive: true });
  }
});

test('a 100 KB ride3d chunk fails with the chunk message', () => {
  const dir = chunkFixture('ride3d', 100 * 1024);
  try {
    const r = spawnSync('node', [script, dir], { encoding: 'utf8' });
    assert.equal(r.status, 1);
    assert.match(r.stderr, /chunk ride3d \d+(\.\d)? KB gz exceeds 25 KB/);
  } finally {
    rmSync(dir, { recursive: true });
  }
});

test('a 10 KB ride3d chunk passes and is reported', () => {
  const dir = chunkFixture('ride3d', 10 * 1024);
  try {
    const out = execFileSync('node', [script, dir], { encoding: 'utf8' });
    assert.match(out, /chunk ride3d \d+(\.\d)? KB gz/);
  } finally {
    rmSync(dir, { recursive: true });
  }
});

test('photo over 30 KB and maplibre over 300 KB fail', () => {
  for (const [name, kb, limit] of [
    ['photo', 40, 30],
    ['maplibre', 320, 300],
  ]) {
    const dir = chunkFixture(name, kb * 1024);
    try {
      const r = spawnSync('node', [script, dir], { encoding: 'utf8' });
      assert.equal(r.status, 1, name);
      assert.match(r.stderr, new RegExp(`chunk ${name} \\d+(\\.\\d)? KB gz exceeds ${limit} KB`));
    } finally {
      rmSync(dir, { recursive: true });
    }
  }
});

test('a missing budgeted chunk fails (a rename would otherwise skip its budget)', () => {
  const dir = fixture(1024, { maplibre: 1024, photo: 1024 });
  try {
    const r = spawnSync('node', [script, dir], { encoding: 'utf8' });
    assert.equal(r.status, 1);
    assert.match(r.stderr, /chunk ride3d not found/);
  } finally {
    rmSync(dir, { recursive: true });
  }
});
