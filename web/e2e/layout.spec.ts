import { expect, test } from './fixtures';
import { completeSetup, expectGoodLayout, watchCsp } from './helpers';

const SIZES = [
  { name: 'phone', width: 360, height: 640 },
  { name: 'tablet', width: 1024, height: 768 },
  { name: 'tv', width: 1920, height: 1080 },
];

const ADMIN_PAGES = [
  '/riders',
  '/history',
  '/settings/general',
  '/settings/bike',
  '/settings/integrations',
  '/settings/diagnostics',
  '/settings/auth',
  '/settings/users',
  '/settings/tokens',
];

// Sizes are set here (the playwright config runs this file in the phone project only).
for (const size of SIZES) {
  test.describe(`${size.name} ${size.width}x${size.height}`, () => {
    test.use({ viewport: { width: size.width, height: size.height }, hasTouch: size.width < 1600 });

    test('every screen fits without sideways scrolling and has big controls', async ({ page }) => {
      const csp = watchCsp(page);

      await page.goto('/setup');
      await expectGoodLayout(page, 'setup');

      await completeSetup(page);
      await expectGoodLayout(page, 'ride (idle)');

      await page.getByRole('button', { name: 'Connect' }).click();
      await expect(page.getByTestId('ride-state')).toHaveText('ready', { timeout: 5_000 });
      await expectGoodLayout(page, 'ride (ready)');
      await page.getByRole('button', { name: 'Start' }).click();
      await expect(page.getByTestId('ride-state')).toHaveText('riding');
      await expectGoodLayout(page, 'ride (riding)');

      // Leave the bike running; the other pages do not need it.
      for (const path of ADMIN_PAGES) {
        await page.goto(path);
        await expect(page.getByRole('navigation', { name: 'Main' })).toBeVisible();
        await expectGoodLayout(page, path);
      }

      await page.goto('/');
      await page.getByRole('button', { name: 'Sign out' }).click();
      await expect(page).toHaveURL(/\/login/);
      await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();
      await expectGoodLayout(page, '/login');

      await page.goto('/kiosk');
      await expectGoodLayout(page, '/kiosk');

      expect(csp.violations).toEqual([]);
    });
  });
}
