import { expect, setupAdmin, test as base } from './fixtures';

// Runs in the tablet project only (see playwright.config.ts). A signed-in admin, in place before the page loads.
const test = base.extend<{ env: void }>({
  env: [
    async ({ context, baseURL }, use) => {
      await context.addCookies(await setupAdmin(baseURL));
      await use();
    },
    { auto: true },
  ],
});

test('build an interval workout, ride it to the end', async ({ page }) => {
  test.setTimeout(90_000);

  // 1. Build: two 10 s blocks, level 4 then level 9.
  await page.goto('/workouts');
  await page.getByRole('link', { name: 'New workout' }).click();
  await expect(page).toHaveURL(/\/workouts\/new$/);
  await page.getByLabel('Name', { exact: true }).fill('E2E intervals');
  await page.getByLabel('Block 1 duration').fill('0:10');
  await page.getByLabel('Block 1 target').selectOption('level');
  await page.getByLabel('Block 1 Level value').fill('4');
  await page.getByRole('button', { name: 'Add block' }).click();
  await page.getByLabel('Block 2 duration').fill('0:10');
  await page.getByLabel('Block 2 target').selectOption('level');
  await page.getByLabel('Block 2 Level value').fill('9');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page).toHaveURL(/\/workouts\/\d+$/);
  await expect(page.getByRole('img', { name: /Workout preview/ }).locator('rect')).toHaveCount(2);

  // 2. Ride it.
  await page.getByRole('link', { name: 'Ride this workout' }).click();
  await expect(page).toHaveURL(/\/\?workout=\d+$/);
  await expect(page.getByLabel('Rider').locator('option:checked')).toHaveText('admin');
  await page.getByRole('button', { name: 'Connect' }).click();
  const state = page.getByTestId('ride-state');
  await expect(state).toHaveText('ready', { timeout: 5_000 });
  await expect(page.getByLabel('Ride', { exact: true })).toHaveValue(/^workout:\d+$/);
  await page.getByRole('button', { name: 'Start' }).click();
  await expect(state).toHaveText('riding');

  // 3. Block 1 at level 4.
  const level = page.getByText('Resistance', { exact: true }).locator('xpath=following-sibling::span[1]');
  await expect(page.getByTestId('block-pos')).toHaveText('1/2', { timeout: 4_000 });
  await expect(level).toHaveText('L4', { timeout: 4_000 });
  await expect(page.getByTestId('block-target')).toHaveText('L4');

  // 4. Block 2 at level 9.
  await expect(page.getByTestId('block-pos')).toHaveText('2/2', { timeout: 15_000 });
  await expect(level).toHaveText('L9', { timeout: 15_000 });
  await expect(page.getByTestId('next-block')).toHaveText('Last block');

  // 5. The workout ends by itself.
  await expect(page.getByText('Workout complete')).toBeVisible({ timeout: 15_000 });
  await expect(state).toHaveText(/ready|finished/);
});

test('free ERG: set a target and nudge it', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Connect' }).click();
  const state = page.getByTestId('ride-state');
  await expect(state).toHaveText('ready', { timeout: 5_000 });
  await page.getByLabel('Ride', { exact: true }).selectOption('erg');
  await page.getByLabel('Target power (W)').fill('200');
  await page.getByRole('button', { name: 'Start' }).click();
  await expect(state).toHaveText('riding');
  const target = page.getByRole('group', { name: 'Ride metrics' }).locator('p', { hasText: /^Target/ }).locator('xpath=following-sibling::div[1]/span[1]');
  await expect(target).toHaveText('200', { timeout: 4_000 });
  await page.getByRole('button', { name: '+10 W' }).click();
  await expect(target).toHaveText('210', { timeout: 4_000 });
  await page.getByLabel('Target power (W)').fill('260');
  await page.getByRole('button', { name: 'Set' }).click();
  await expect(target).toHaveText('260', { timeout: 4_000 });
});
