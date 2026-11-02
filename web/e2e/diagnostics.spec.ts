import { apiAs, expect, setupAdmin, test as base } from './fixtures';

// Tablet only: the page is the same at every size, and a scan holds the adapter.
const test = base.extend<{ cookies: Awaited<ReturnType<typeof setupAdmin>> }>({
  cookies: async ({ context, baseURL }, use) => {
    const cookies = await setupAdmin(baseURL);
    await context.addCookies(cookies);
    await use(cookies);
  },
});
test.beforeEach(({}, info) => {
  test.skip(info.project.name !== 'tablet', 'tablet project only');
});

// CI has no Bluetooth, so nothing here asserts a specific adapter outcome.
test('simulator: no Bluetooth in use', async ({ page, cookies }) => {
  void cookies;
  await page.goto('/settings');
  await page.getByRole('link', { name: 'Diagnostics' }).click();
  await expect(page).toHaveURL(/\/settings\/diagnostics$/);
  await expect(page.getByText('No Bluetooth in use (simulator or Home Assistant driver)')).toBeVisible({
    timeout: 25_000,
  });
});

test('local adapter card shows a badge and text, and a scan completes', async ({ page, cookies, baseURL }) => {
  await apiAs(baseURL, cookies, 'PATCH', '/api/settings', { bike: { driver: 'ftms', transport: 'local' } });
  await page.goto('/settings/general');
  await page.getByRole('link', { name: 'Diagnostics' }).click();

  const card = page.getByRole('region', { name: 'Local adapter' });
  await expect(card).toBeVisible({ timeout: 25_000 });
  await expect(card.getByText(/^(ok|error)$/)).toBeVisible();
  const text = (await card.locator('p').first().textContent()) ?? '';
  expect(text.trim()).not.toBe('');

  const scan = page.getByRole('button', { name: 'Scan for signal strength' });
  await scan.click();
  await expect(scan).toBeEnabled({ timeout: 10_000 });
  await expect(page.getByRole('alert')).toHaveCount(0);
});
