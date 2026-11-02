// Fails when the main bundle (entry JS + CSS referenced by index.html)
// exceeds 400 KB gzipped, or a budgeted lazy chunk (maplibre, mlworker, ride3d, photo)
// exceeds its own limit. Lazy chunks are not referenced from index.html and
// so are not part of the main total.
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { gzipSync } from 'node:zlib';

export const LIMIT_BYTES = 409600;

/** Lazy chunk name (assets/<name>-<hash>.js) -> gzipped limit in KB. */
export const CHUNK_LIMITS_KB = { maplibre: 300, mlworker: 130, ride3d: 25, photo: 30 };

function attrs(tag) {
  const out = {};
  for (const m of tag.matchAll(/([a-zA-Z-]+)\s*=\s*"([^"]*)"|([a-zA-Z-]+)\s*=\s*'([^']*)'/g)) {
    out[(m[1] ?? m[3]).toLowerCase()] = m[2] ?? m[4];
  }
  return out;
}

/** Gzipped (level 9) byte total of every asset index.html loads eagerly. */
export function mainBundleBytes(distDir) {
  const html = readFileSync(join(distDir, 'index.html'), 'utf8');
  const refs = new Set();
  for (const [tag] of html.matchAll(/<script\b[^>]*>/gi)) {
    const a = attrs(tag);
    if (a.type === 'module' && a.src) refs.add(a.src);
  }
  for (const [tag] of html.matchAll(/<link\b[^>]*>/gi)) {
    const a = attrs(tag);
    if (a.href && /^(stylesheet|modulepreload)$/i.test(a.rel ?? '')) refs.add(a.href);
  }
  let total = 0;
  for (const ref of refs) {
    const file = join(distDir, ref.replace(/^\//, ''));
    total += gzipSync(readFileSync(file), { level: 9 }).length;
  }
  return total;
}

/** Gzipped (level 9) bytes of assets/<name>-<hash>.js; 0 when the build has no such chunk. */
export function chunkBytes(distDir, name) {
  const re = new RegExp(`^${name}-[A-Za-z0-9_-]+\\.js$`);
  let total = 0;
  for (const f of readdirSync(join(distDir, 'assets'))) {
    if (re.test(f)) total += gzipSync(readFileSync(join(distDir, 'assets', f)), { level: 9 }).length;
  }
  return total;
}

const self = fileURLToPath(import.meta.url);
if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === pathToFileURL(self).href) {
  const dist = process.argv[2] ?? resolve(dirname(self), '../../internal/web/dist');
  const failures = [];
  const bytes = mainBundleBytes(dist);
  console.log(`main bundle ${(bytes / 1024).toFixed(1)} KB gz`);
  if (bytes > LIMIT_BYTES) failures.push(`main bundle ${(bytes / 1024).toFixed(1)} KB gz exceeds 400 KB`);
  for (const [name, limit] of Object.entries(CHUNK_LIMITS_KB)) {
    const n = chunkBytes(dist, name);
    if (n === 0) {
      failures.push(`chunk ${name} not found in assets/ (renamed? update CHUNK_LIMITS_KB and vite.config.ts)`);
      continue;
    }
    const kb = (n / 1024).toFixed(1);
    console.log(`chunk ${name} ${kb} KB gz`);
    if (n > limit * 1024) failures.push(`chunk ${name} ${kb} KB gz exceeds ${limit} KB`);
  }
  if (failures.length) {
    for (const f of failures) console.error(f);
    process.exit(1);
  }
}
