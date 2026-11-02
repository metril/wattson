import { readFileSync } from 'node:fs';
import type { Page } from '@playwright/test';
import { expect, setupAdmin, test } from './fixtures';
import { metric } from './helpers';

// Ride the simulator for ~30 s, then find the ride in History, look at its
// charts and download the FIT file, first with a browser session and then from
// a paired display (which has only a Bearer token, so it saves a fetched blob).
// No external network is used: there is no Strava app and no route, so no map.

/** The saved file is a FIT file: a header of at least 12 bytes with ".FIT" at bytes 8..12. */
async function expectFit(page: Page, trigger: () => Promise<void>) {
  const [download] = await Promise.all([page.waitForEvent('download'), trigger()]);
  expect(download.suggestedFilename()).toMatch(/^wattson-\d+-\d{8}-\d{4}\.fit$/);
  const path = await download.path();
  const bytes = readFileSync(path);
  expect(bytes.length).toBeGreaterThan(100);
  expect(bytes.subarray(8, 12).toString('latin1')).toBe('.FIT');
}

test('a simulated ride appears in history and downloads as a FIT file', async ({ page, context, baseURL }, info) => {
  test.skip(info.project.name !== 'tablet', 'a 30 s ride; one project covers the flow');
  test.setTimeout(90_000);
  await context.addCookies(await setupAdmin(baseURL));

  await test.step('ride for 30 seconds', async () => {
    await page.goto('/');
    const state = page.getByTestId('ride-state');
    await expect(state).toHaveText('idle');
    await expect(page.getByLabel('Rider').locator('option:checked')).toHaveText('admin');
    await page.getByRole('button', { name: 'Connect' }).click();
    await expect(state).toHaveText('ready', { timeout: 5_000 });
    await page.getByRole('button', { name: 'Start' }).click();
    await expect(state).toHaveText('riding');
    // The Time tile reads 00:00:SS; wait for 30 s of riding.
    await expect(metric(page, 'Time')).toHaveText(/^00:00:(3\d|[4-5]\d)$/, { timeout: 45_000 });
    await page.getByRole('button', { name: 'Stop', exact: true }).click();
    await page.getByRole('button', { name: 'Tap again to stop' }).click();
    await expect(state).toHaveText('ready', { timeout: 6_000 });
  });

  await test.step('the ride is listed', async () => {
    await page.goto('/history');
    const row = page.getByRole('link').filter({ hasText: 'Free ride' });
    await expect(row).toHaveCount(1, { timeout: 10_000 });
    await expect(row).toContainText(/00:00:3\d(?!\d)/);
    await row.click();
    await expect(page).toHaveURL(/\/history\/\d+$/);
  });

  await test.step('detail shows charts and stats', async () => {
    await expect(page.getByRole('heading', { name: 'Free ride' })).toBeVisible();
    await expect(page.getByRole('img', { name: /^Power over distance/ })).toBeVisible();
    // The simulator holds 130 W at the default level; the first seconds ramp up, so the average sits just under.
    await expect(page.locator('dt', { hasText: /^Avg power$/ }).locator('xpath=following-sibling::dd[1]')).toHaveText(/^1[23]\d\s*W$/);
  });

  await test.step('download the FIT file with the browser session', async () => {
    await expectFit(page, () => page.getByRole('link', { name: 'Download FIT' }).click());
  });

  await test.step('Strava upload explains that the rider is not connected', async () => {
    await page.getByRole('button', { name: 'Upload to Strava' }).click();
    await expect(page.getByText('Connect Strava for this rider on the Riders page.')).toBeVisible();
  });

  await test.step('a paired display opens history and downloads the FIT through the Bearer blob path', async () => {
    const created = await page.request.post('/api/tokens', { data: { name: 'e2e history kiosk', scopes: ['read', 'control'] } });
    expect(created.ok()).toBe(true);
    const { token } = (await created.json()) as { token: string };
    await context.clearCookies();
    await page.goto(`/kiosk#token=${token}`);
    await expect(page.getByTestId('ride-state')).toBeVisible();
    expect(await page.evaluate(() => localStorage.getItem('wattson.kiosk_token'))).not.toBeNull();

    await page.getByRole('link', { name: 'History' }).first().click();
    await expect(page).toHaveURL(/\/history$/);
    await page.getByRole('link').filter({ hasText: 'Free ride' }).click();
    await expect(page).toHaveURL(/\/history\/\d+$/);
    // A paired display gets a button (it fetches with the token), not a plain link.
    await expect(page.getByRole('link', { name: 'Download FIT' })).toHaveCount(0);
    await expectFit(page, () => page.getByRole('button', { name: 'Download FIT' }).click());
  });
});
