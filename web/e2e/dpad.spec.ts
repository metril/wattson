import { expect, test } from './fixtures';
import { completeSetup } from './helpers';

// The remote has arrows, Enter and Escape only. tv project only (the phone/tablet projects use touch).
test('start picker: from the Ride select to Start with arrows, Enter and Escape only', async ({ page }, info) => {
  test.skip(info.project.name !== 'tv', 'a remote-control flow; one project covers it');
  await completeSetup(page);
  await page.getByRole('button', { name: 'Connect' }).click();
  await expect(page.getByTestId('ride-state')).toHaveText('ready', { timeout: 5_000 });
  const start = page.getByRole('button', { name: 'Start' });
  await expect(start).toBeFocused();

  const rideSelect = page.getByLabel('Ride', { exact: true });

  // Reach the Ride select by arrows, pick Free ERG with Down (native), then leave the select to the right.
  // A remote user tries arrows until the wanted control has focus (Left/Right always move on; Up/Down stay native in a select).
  const walk = async (keys: string[], until: () => Promise<boolean>) => {
    for (let i = 0; i < 16 && !(await until()); i++) {
      const onSelect = (await page.evaluate(() => document.activeElement?.tagName)) === 'SELECT';
      await page.keyboard.press(onSelect ? 'ArrowLeft' : keys[i % keys.length]);
    }
    expect(await until()).toBe(true);
  };
  await walk(['ArrowLeft'], () => rideSelect.evaluate((el) => el === document.activeElement));
  await page.keyboard.press('ArrowDown');
  await expect(rideSelect).toHaveValue('erg');
  await page.keyboard.press('ArrowRight');
  expect(await page.evaluate(() => document.activeElement?.tagName)).not.toBe('SELECT');

  // Reach the target field, type, Escape leaves it (value kept), come back and Enter moves on to Start.
  const watts = page.getByLabel('Target power (W)');
  await walk(['ArrowLeft'], () => watts.evaluate((el) => el === document.activeElement));
  await page.keyboard.press('Control+A');
  await page.keyboard.type('210');
  await page.keyboard.press('Escape');
  await expect(watts).not.toBeFocused();
  await expect(watts).toHaveValue('210');
  await watts.focus();
  await page.keyboard.press('Enter');
  await expect(start).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('ride-state')).toHaveText('riding');
});
