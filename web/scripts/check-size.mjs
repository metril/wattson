// Fails when the main bundle (entry JS + CSS referenced by index.html)
// exceeds 400 KB gzipped. Lazy chunks are not referenced from index.html
// and so are not counted.
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { gzipSync } from 'node:zlib';

export const LIMIT_BYTES = 409600;

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

const self = fileURLToPath(import.meta.url);
if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === pathToFileURL(self).href) {
  const dist = process.argv[2] ?? resolve(dirname(self), '../../internal/web/dist');
  const bytes = mainBundleBytes(dist);
  const kb = (bytes / 1024).toFixed(1);
  if (bytes > LIMIT_BYTES) {
    console.error(`main bundle ${kb} KB gz exceeds 400 KB`);
    process.exit(1);
  }
  console.log(`main bundle ${kb} KB gz`);
}
