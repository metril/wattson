import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';
import { apiAs, expect, importRamp, setupAdmin, stubMapNetwork, test as base } from './fixtures';
import { expectGoodLayout, levelText } from './helpers';

// Runs in the phone, tablet and tv projects at their own viewports: big targets, no sideways
// scrolling and no serious axe violations on the main screens, plus a remote-control-only ride on the tv.
const test = base.extend<{ signedIn: void }>({
  signedIn: [
    async ({ context, baseURL }, use) => {
      const cookies = await setupAdmin(baseURL);
      await apiAs(baseURL, cookies, 'POST', '/api/riders', { name: 'Guest' });
      await context.addCookies(cookies);
      await stubMapNetwork(context);
      await importRamp(baseURL, cookies);
      await use();
    },
    { auto: true },
  ],
});

async function expectNoSeriousViolations(page: Page, label: string) {
  await page.waitForLoadState('networkidle');
  const { violations } = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  const bad = violations
    .filter((v) => v.impact === 'serious' || v.impact === 'critical')
    .map((v) => `${v.id} (${v.impact}): ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`);
  expect(bad, `axe on ${label}`).toEqual([]);
}

async function checkPage(page: Page, label: string) {
  await expectGoodLayout(page, label);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${label} scrolls sideways`).toBe(true);
  await expectNoSeriousViolations(page, label);
}

test('ride, routes, history and integrations: big targets, no sideways scroll, no serious a11y violations', async ({ page }) => {
  test.setTimeout(90_000);
  await page.goto('/');
  await expect(page.getByTestId('ride-state')).toBeVisible();
  await checkPage(page, 'ride (idle)');

  await page.getByRole('button', { name: 'Connect' }).click();
  await expect(page.getByTestId('ride-state')).toHaveText('ready', { timeout: 5_000 });
  await page.getByRole('button', { name: 'Start' }).click();
  await expect(page.getByTestId('ride-state')).toHaveText('riding');
  await checkPage(page, 'ride (riding)');

  for (const [path, loaded] of [
    ['/routes', page.getByRole('heading', { name: /Routes/ }).first()],
    ['/history', page.getByRole('heading', { name: /History/ }).first()],
    ['/settings/integrations', page.getByLabel('Mapillary access token')],
  ] as const) {
    await page.goto(path);
    await expect(page.getByRole('navigation', { name: 'Main' })).toBeVisible();
    await expect(loaded).toBeVisible();
    await checkPage(page, path);
  }
});

test('a remote control alone rides: pick the rider, connect, start, level 6, pause, stop', async ({ page }, info) => {
  test.skip(info.project.name !== 'tv', 'a remote-control flow; one project covers it');
  await page.goto('/');
  const state = page.getByTestId('ride-state');
  await expect(state).toHaveText('idle');
  const rider = page.getByLabel('Rider');

  // Arrows only (Tab is never pressed): walk until the wanted control has focus.
  const focus = async (want: () => Promise<boolean>, keys = ['ArrowLeft', 'ArrowRight', 'ArrowDown', 'ArrowUp']) => {
    for (let i = 0; i < 24 && !(await want()); i++) {
      const onSelect = await page.evaluate(() => document.activeElement?.tagName === 'SELECT');
      await page.keyboard.press(onSelect ? 'ArrowRight' : keys[Math.floor(i / 6) % keys.length]);
    }
    expect(await want()).toBe(true);
  };
  const focused = (name: string) => () => page.getByRole('button', { name, exact: true }).evaluate((el) => el === document.activeElement);

  await focus(() => rider.evaluate((el) => el === document.activeElement));
  await page.keyboard.press('ArrowDown'); // native: next rider
  await expect(rider.locator('option:checked')).toHaveText('Guest');

  await focus(focused('Connect'));
  await page.keyboard.press('Enter');
  await expect(state).toHaveText('ready', { timeout: 5_000 });

  const start = page.getByRole('button', { name: 'Start', exact: true });
  await expect(start).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(state).toHaveText('riding');
  await expect(page.getByRole('button', { name: 'Pause', exact: true })).toBeFocused();

  // Each Up raises the level by one once the bike confirms; wait for it before pressing again.
  const level = async () => Number(/^L(\d+)/.exec((await levelText(page).textContent()) ?? '')?.[1] ?? 0);
  for (let now = await level(); now < 6; now = await level()) {
    await page.keyboard.press('ArrowUp');
    await expect.poll(level, { timeout: 5_000 }).toBeGreaterThan(now);
  }
  await expect(levelText(page)).toHaveText(/^L6/);

  await page.keyboard.press('Enter'); // Pause
  await expect(state).toHaveText('paused');
  await focus(focused('Stop'), ['ArrowRight', 'ArrowDown']);
  await page.keyboard.press('Enter');
  await expect(page.getByRole('button', { name: 'Tap again to stop' })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(state).toHaveText('ready', { timeout: 6_000 });
});
