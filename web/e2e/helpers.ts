import { expect, type Locator, type Page } from '@playwright/test';

export const ADMIN = { username: 'admin', password: 'correct-horse-1' };

/** Records CSP violations and WebSockets so tests can assert on them. */
export function watchCsp(page: Page) {
  const violations: string[] = [];
  const sockets: string[] = [];
  page.on('console', (m) => {
    if (/content security policy/i.test(m.text())) violations.push(m.text());
  });
  page.on('websocket', (ws) => sockets.push(ws.url()));
  return { violations, sockets };
}

/** `/` -> /setup wizard: admin account, skip the rest, land on the ride screen. */
export async function completeSetup(page: Page) {
  await page.goto('/');
  await expect(page).toHaveURL(/\/setup$/);
  await page.getByLabel('Username').fill(ADMIN.username);
  await page.getByLabel('Password', { exact: true }).fill(ADMIN.password);
  await page.getByLabel('Confirm password').fill(ADMIN.password);
  await page.getByRole('button', { name: 'Next' }).click();

  await expect(page.getByRole('heading', { name: 'Public URL' })).toBeVisible();
  await page.getByRole('button', { name: 'Skip' }).click();

  await expect(page.getByRole('heading', { name: 'Bike connection' })).toBeVisible();
  // Keep the simulator. Save is only enabled after an edit, so change one harmless field.
  await expect(page.getByLabel('Driver')).toHaveValue('sim');
  await page.getByLabel('Idle auto-release (minutes)').fill('20');
  await page.getByRole('button', { name: 'Save' }).click();

  await expect(page.getByRole('heading', { name: 'Sign-in provider' })).toBeVisible();
  await page.getByRole('button', { name: 'Skip' }).click();

  await expect(page.getByRole('heading', { name: 'All set' })).toBeVisible();
  await page.getByRole('button', { name: 'Open Wattson' }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByTestId('ride-state')).toBeVisible();
}

/** The text of the metric tile whose label is `name` (Power, Cadence, ...). */
export function metric(page: Page, name: string): Locator {
  return page
    .getByRole('group', { name: 'Ride metrics' })
    .locator('p', { hasText: new RegExp(`^${name}`) })
    .locator('xpath=following-sibling::div[1]/span[1]');
}

/** On a phone only four metrics show; opens the rest. A no-op on wider screens. */
export async function showAllMetrics(page: Page) {
  const more = page.getByRole('button', { name: 'More metrics' });
  if ((await more.count()) > 0 && (await more.getAttribute('aria-expanded')) === 'false') await more.click();
}

/** The big resistance number between the − and + buttons. */
export function levelText(page: Page): Locator {
  return page.getByText('Resistance', { exact: true }).locator('xpath=following-sibling::span[1]');
}

const MIN_TOUCH = 48;

/** Interactive controls smaller than 48 px, and whether the page scrolls sideways. */
export async function layoutProblems(page: Page): Promise<string[]> {
  return page.evaluate((min) => {
    const out: string[] = [];
    const de = document.documentElement;
    if (de.scrollWidth > de.clientWidth) out.push(`horizontal overflow: scrollWidth ${de.scrollWidth} > ${de.clientWidth}`);
    if (document.body.scrollWidth > de.clientWidth) out.push(`body overflows: ${document.body.scrollWidth} > ${de.clientWidth}`);
    const sel = 'button, a[href], select, input:not([type=hidden]), textarea, [role=button], [role=switch], [role=tab]';
    for (const el of document.querySelectorAll<HTMLElement>(sel)) {
      // A checkbox, radio or file input is hit through its label; measure that instead.
      const target = el instanceof HTMLInputElement && /checkbox|radio|file/.test(el.type) ? (el.closest('label') ?? el) : el;
      const r = target.getBoundingClientRect();
      const cs = getComputedStyle(target);
      if (r.width === 0 || r.height === 0 || cs.visibility === 'hidden' || cs.display === 'none') continue;
      if ((r.left < -0.5 || r.right > de.clientWidth + 0.5) && !inXScroller(el)) out.push(`off-screen horizontally: ${describe(el)} ${r.left | 0}..${r.right | 0}`);
      if (r.width < min - 0.5 || r.height < min - 0.5) out.push(`small control ${r.width | 0}x${r.height | 0}: ${describe(el)}`);
    }
    return out;

    // A tab strip that scrolls on its own may extend past the screen.
    function inXScroller(el: HTMLElement) {
      for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
        if (/auto|scroll/.test(getComputedStyle(p).overflowX)) return true;
      }
      return false;
    }

    function describe(el: HTMLElement) {
      const name = el.getAttribute('aria-label') ?? el.textContent?.trim().slice(0, 30) ?? '';
      return `<${el.tagName.toLowerCase()}> "${name}"`;
    }
  }, MIN_TOUCH);
}

/** Fails with every problem found on the current page, labelled. */
export async function expectGoodLayout(page: Page, label: string) {
  await page.waitForLoadState('networkidle');
  expect(await layoutProblems(page), `layout of ${label}`).toEqual([]);
}
