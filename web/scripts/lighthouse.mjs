// Lighthouse (mobile, simulated throttling) against a real `wattson serve`:
// the sign-in page and the idle ride screen, five runs each, median per
// metric, checked against lighthouse-budgets.json (a page over budget is re-run once). Run `make build` first.
//
//   npm run perf
//
// Writes lighthouse-report/*.html and exits 1 listing every failed budget as
// "<page> <metric> <value> < <min>" (or "> <max>").
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const ADMIN = { username: 'admin', password: 'correct-horse-1' };

export function median(xs) {
  const s = [...xs].sort((a, b) => a - b);
  const m = s.length >> 1;
  return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2;
}

/** Numbers one Lighthouse result contributes: category scores, LCP (ms), CLS and script transfer (KB). */
export function metricsOf(lhr) {
  const score = (id) => lhr.categories[id]?.score ?? 0;
  const scripts = (lhr.audits['resource-summary']?.details?.items ?? []).find((i) => i.resourceType === 'script');
  return {
    performance: score('performance'),
    accessibility: score('accessibility'),
    'best-practices': score('best-practices'),
    lcpMs: lhr.audits['largest-contentful-paint']?.numericValue ?? Infinity,
    cls: lhr.audits['cumulative-layout-shift']?.numericValue ?? Infinity,
    scriptKB: (scripts?.transferSize ?? 0) / 1024,
    benchmark: lhr.environment?.benchmarkIndex ?? 0,
  };
}

/**
 * Lighthouse's own CPU benchmark (benchmarkIndex) says how fast this machine is; simulated throttling
 * scales the page's measured CPU time, so a slow runner reads slower LCP. The LCP budget is multiplied by
 * referenceBenchmark / benchmark (never below 1, at most maxLcpScale) so a slow runner is not failed for
 * being slow. Everything else keeps its budget. A benchmark of 0 (unknown) scales nothing.
 */
export function lcpScale(benchmark, budgets) {
  const { referenceBenchmark = 1000, maxLcpScale = 1.5 } = budgets;
  if (!(benchmark > 0)) return 1;
  return Math.min(maxLcpScale, Math.max(1, referenceBenchmark / benchmark));
}

/**
 * Failed budgets for `runs` (arrays of metricsOf, per page path). Scores, LCP and CLS are the median
 * of the runs (LCP against the benchmark-scaled budget); script transfer is judged on "/" only.
 */
export function failures(byPage, budgets) {
  const out = [];
  const fmt = (n) => (Number.isInteger(n) ? String(n) : n.toFixed(n < 1 ? 3 : 1));
  for (const [page, runs] of Object.entries(byPage)) {
    const med = (k) => median(runs.map((r) => r[k]));
    for (const [name, min] of Object.entries(budgets.scores)) {
      if (med(name) < min) out.push(`${page} ${name} ${fmt(med(name))} < ${min}`);
    }
    const lcpMax = budgets.lcpMs * lcpScale(med('benchmark'), budgets);
    if (med('lcpMs') > lcpMax) out.push(`${page} LCP ${fmt(med('lcpMs'))} ms > ${Math.round(lcpMax)} ms`);
    if (med('cls') > budgets.cls) out.push(`${page} CLS ${fmt(med('cls'))} > ${budgets.cls}`);
    if (page === '/' && med('scriptKB') > budgets.scriptTransferKB) {
      out.push(`${page} script transfer ${fmt(med('scriptKB'))} KB > ${budgets.scriptTransferKB} KB`);
    }
  }
  return out;
}

async function freePort() {
  const s = createServer();
  s.listen(0, '127.0.0.1');
  await once(s, 'listening');
  const { port } = s.address();
  s.close();
  await once(s, 'close');
  return port;
}

async function waitHealthy(url, proc, log) {
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    if (proc.exitCode !== null) throw new Error(`wattson exited with ${proc.exitCode}\n${log()}`);
    try {
      if ((await fetch(`${url}/healthz`)).ok) return;
    } catch {
      /* not listening yet */
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error(`wattson did not become healthy\n${log()}`);
}

async function main() {
  const budgets = JSON.parse(readFileSync(resolve(here, '../lighthouse-budgets.json'), 'utf8'));
  const binary = resolve(here, '../../wattson');
  const reports = resolve(here, '../lighthouse-report');
  rmSync(reports, { recursive: true, force: true });
  mkdirSync(reports, { recursive: true });

  const dataDir = mkdtempSync(join(tmpdir(), 'wattson-perf-'));
  const profile = mkdtempSync(join(tmpdir(), 'wattson-perf-chrome-'));
  const port = await freePort();
  const debugPort = await freePort();
  const url = `http://127.0.0.1:${port}`;
  let out = '';
  const server = spawn(binary, ['serve', '--data-dir', dataDir, '--listen', `127.0.0.1:${port}`], {
    env: Object.fromEntries(Object.entries(process.env).filter(([k]) => !k.startsWith('WATTSON_'))),
  });
  server.stdout.on('data', (d) => (out += d));
  server.stderr.on('data', (d) => (out += d));

  const [{ default: lighthouse }, { chromium }] = await Promise.all([import('lighthouse'), import('@playwright/test')]);
  let context;
  let failed = [];
  try {
    await waitHealthy(url, server, () => out);
    const setup = await fetch(`${url}/api/setup`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(ADMIN),
    });
    if (setup.status !== 201) throw new Error(`setup failed: ${setup.status} ${await setup.text()}`);

    // A persistent context is the browser's default one, which Lighthouse's own tab shares (so it keeps our sign-in).
    context = await chromium.launchPersistentContext(profile, { args: [`--remote-debugging-port=${debugPort}`] });
    const once = async (path) => {
      const runs = [];
      for (let i = 0; i < budgets.runs; i++) {
        const r = await lighthouse(url + path, {
          port: debugPort,
          output: 'html',
          onlyCategories: ['performance', 'accessibility', 'best-practices'],
          formFactor: 'mobile',
          throttlingMethod: 'simulate',
          logLevel: 'error',
        });
        const landed = new URL(r.lhr.finalDisplayedUrl).pathname;
        if (landed !== path) throw new Error(`${path} was audited as ${landed}: the sign-in state is not what the audit needs`);
        runs.push({ m: metricsOf(r.lhr), html: r.report });
      }
      return runs;
    };
    const audit = async (path) => {
      let runs = await once(path);
      const bad = failures({ [path]: runs.map((r) => r.m) }, budgets);
      if (bad.length) {
        console.log(`${path}: over budget (${bad.join('; ')}), retrying once`);
        runs = await once(path);
      }
      const sorted = [...runs].sort((a, b) => a.m.performance - b.m.performance);
      const best = sorted[sorted.length >> 1];
      const name = path === '/' ? 'ride' : path.slice(1);
      writeFileSync(join(reports, `${name}.html`), best.html);
      const m = best.m;
      console.log(
        `${path}: perf ${m.performance} a11y ${m.accessibility} bp ${m['best-practices']} LCP ${Math.round(m.lcpMs)} ms CLS ${m.cls.toFixed(3)} scripts ${m.scriptKB.toFixed(1)} KB ` +
          `benchmarkIndex ${Math.round(median(runs.map((r) => r.m.benchmark)))} (LCP budget x${lcpScale(median(runs.map((r) => r.m.benchmark)), budgets).toFixed(2)})`,
      );
      return runs.map((r) => r.m);
    };

    const byPage = {};
    // "/login" is audited signed out, so it goes first; then sign in for the ride screen.
    for (const path of budgets.pages.filter((p) => p === '/login')) byPage[path] = await audit(path);
    const page = context.pages()[0] ?? (await context.newPage());
    await page.goto(`${url}/login`);
    await page.getByLabel('Username').fill(ADMIN.username);
    await page.getByLabel('Password', { exact: true }).fill(ADMIN.password);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await page.getByTestId('ride-state').waitFor();
    for (const path of budgets.pages.filter((p) => p !== '/login')) byPage[path] = await audit(path);
    failed = failures(byPage, budgets);
  } finally {
    await context?.close();
    server.kill('SIGTERM');
    await once(server, 'exit').catch(() => {});
    rmSync(dataDir, { recursive: true, force: true });
    rmSync(profile, { recursive: true, force: true });
  }
  if (failed.length) {
    for (const f of failed) console.error(f);
    process.exit(1);
  }
  console.log('lighthouse budgets met');
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  main().catch((e) => {
    console.error(e);
    process.exit(1);
  });
}
