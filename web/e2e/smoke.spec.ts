import { expect, test } from './fixtures';
import { completeSetup, levelText, metric, watchCsp } from './helpers';

test('first run to a simulated ride', async ({ page }) => {
  const csp = watchCsp(page);
  await completeSetup(page);

  const state = page.getByTestId('ride-state');
  await expect(state).toHaveText('idle');

  // Connect the simulator as the only rider.
  await expect(page.getByLabel('Rider')).toHaveValue(/\d+/);
  await expect(page.getByLabel('Rider').locator('option:checked')).toHaveText('admin');
  await page.getByRole('button', { name: 'Connect' }).click();
  await expect(state).toHaveText('ready', { timeout: 5_000 });

  // The live link works under the page's own CSP (connect-src 'self').
  expect(csp.sockets.length).toBeGreaterThan(0);
  expect(csp.sockets.every((u) => new URL(u).host === new URL(page.url()).host)).toBe(true);
  await expect(page.getByText('Server link up')).toBeVisible();

  await page.getByRole('button', { name: 'Start' }).click();
  await expect(state).toHaveText('riding');
  await expect(metric(page, 'Power')).toHaveText('130', { timeout: 4_000 });

  const plus = page.getByRole('button', { name: 'Increase level' });
  const minus = page.getByRole('button', { name: 'Decrease level' });
  await plus.click();
  await expect(levelText(page)).toHaveText('L6');
  await expect(metric(page, 'Power')).toHaveText('150', { timeout: 4_000 });

  // Both level buttons are on screen and big enough to hit.
  const viewport = page.viewportSize()!;
  for (const b of [plus, minus]) {
    await b.scrollIntoViewIfNeeded();
    const box = (await b.boundingBox())!;
    expect(box.width).toBeGreaterThanOrEqual(48);
    expect(box.height).toBeGreaterThanOrEqual(48);
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.y).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(viewport.width);
    expect(box.y + box.height).toBeLessThanOrEqual(viewport.height);
  }

  // Stop takes two taps.
  await page.getByRole('button', { name: 'Stop', exact: true }).click();
  await page.getByRole('button', { name: 'Tap again to stop' }).click();
  await expect(state).toHaveText('ready', { timeout: 6_000 });

  await page.getByRole('button', { name: 'Release' }).click();
  await expect(state).toHaveText('idle');

  // The session cookie survives a reload.
  await page.reload();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByTestId('ride-state')).toHaveText('idle');

  expect(csp.violations).toEqual([]);
});
