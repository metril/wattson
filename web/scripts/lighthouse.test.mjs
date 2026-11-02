import assert from 'node:assert/strict';
import { test } from 'node:test';
import { failures, median, metricsOf } from './lighthouse.mjs';

const budgets = {
  scores: { performance: 0.85, accessibility: 0.95, 'best-practices': 0.95 },
  lcpMs: 2500,
  cls: 0.05,
  scriptTransferKB: 400,
};
const good = { performance: 0.95, accessibility: 1, 'best-practices': 1, lcpMs: 1200, cls: 0, scriptKB: 150 };

test('median of odd and even lists', () => {
  assert.equal(median([3, 1, 2]), 2);
  assert.equal(median([4, 1, 3, 2]), 2.5);
});

test('budgets met: nothing is reported', () => {
  assert.deepEqual(failures({ '/login': [good, good, good], '/': [good, good, good] }, budgets), []);
});

test('the median decides, so one bad run is tolerated and two are not', () => {
  const slow = { ...good, performance: 0.5 };
  assert.deepEqual(failures({ '/': [good, slow, good] }, budgets), []);
  assert.deepEqual(failures({ '/': [slow, slow, good] }, budgets), ['/ performance 0.500 < 0.85']);
});

test('each failed assertion is listed as <page> <metric> <value> < <min>', () => {
  const bad = { performance: 0.8, accessibility: 0.9, 'best-practices': 0.9, lcpMs: 3000, cls: 0.2, scriptKB: 500 };
  assert.deepEqual(failures({ '/': [bad] }, budgets), [
    '/ performance 0.800 < 0.85',
    '/ accessibility 0.900 < 0.95',
    '/ best-practices 0.900 < 0.95',
    '/ LCP 3000 ms > 2500 ms',
    '/ CLS 0.200 > 0.05',
    '/ script transfer 500 KB > 400 KB',
  ]);
});

test('script transfer is judged on / only', () => {
  assert.deepEqual(failures({ '/login': [{ ...good, scriptKB: 900 }] }, budgets), []);
});

test('metricsOf reads scores, LCP, CLS and script bytes from a result', () => {
  const m = metricsOf({
    categories: { performance: { score: 0.9 }, accessibility: { score: 1 }, 'best-practices': { score: 0.96 } },
    audits: {
      'largest-contentful-paint': { numericValue: 1500 },
      'cumulative-layout-shift': { numericValue: 0.01 },
      'resource-summary': { details: { items: [{ resourceType: 'font', transferSize: 1 }, { resourceType: 'script', transferSize: 2048 }] } },
    },
  });
  assert.deepEqual(m, { performance: 0.9, accessibility: 1, 'best-practices': 0.96, lcpMs: 1500, cls: 0.01, scriptKB: 2 });
});
