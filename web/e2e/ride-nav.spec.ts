import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, setupAdmin, test as base } from './fixtures';
import { expectGoodLayout } from './helpers';

// The blank style plus one source with attribution, so MapLibre draws its attribution control.
const blank = JSON.parse(readFileSync(resolve(import.meta.dirname, 'fixtures/blank-style.json'), 'utf8'));
const STYLE = JSON.stringify({
  ...blank,
  layers: [...blank.layers, { id: 'credit-fill', type: 'fill', source: 'credit', paint: { 'fill-color': '#000' } }],
  sources: { ...blank.sources, credit: { type: 'geojson', data: { type: 'FeatureCollection', features: [] }, attribution: '© E2E map data, © OpenMapTiles, © OpenStreetMap contributors' } },
});

// A short climbing GPX, uploaded before the page loads (no routing service needed).
const GPX = `<?xml version="1.0"?><gpx version="1.1" creator="e2e" xmlns="http://www.topografix.com/GPX/1/1"><trk><name>Nav</name><trkseg>${Array.from(
  { length: 80 },
  (_, i) => `<trkpt lat="${45.5 + i * 0.0004}" lon="${7.5 + i * 0.0003}"><ele>${300 + i * 2 + Math.sin(i / 5) * 8}</ele></trkpt>`,
).join('')}</trkseg></trk></gpx>`;

const test = base.extend<{ routeId: number }>({
  routeId: async ({ page, context, baseURL }, use) => {
    const cookies = await setupAdmin(baseURL);
    await context.addCookies(cookies);
    await page.route('https://tiles.openfreemap.org/**', (route) =>
      route.request().url().endsWith('/styles/liberty')
        ? route.fulfill({ status: 200, contentType: 'application/json', body: STYLE })
        : route.fulfill({ status: 404, body: '' }),
    );
    const fd = new FormData();
    fd.append('file', new Blob([GPX]), 'nav.gpx');
    const res = await fetch(`${baseURL}/api/routes/import`, {
      method: 'POST',
      headers: { cookie: cookies.map((c) => `${c.name}=${c.value}`).join('; ') },
      body: fd,
    });
    if (!res.ok) throw new Error(`import failed: ${res.status}`);
    await use((await res.json()).id);
  },
});

test('route ride: map controls are 48 px targets and the D-pad drives the ride', async ({ page, routeId }, info) => {
  await page.goto(`/?route=${routeId}`);
  await page.getByRole('button', { name: 'Connect' }).click();
  const state = page.getByTestId('ride-state');
  await expect(state).toHaveText('ready', { timeout: 5_000 });

  // D-pad: focus starts on Start; Enter starts; Pause is focused; Right moves to Stop.
  const start = page.getByRole('button', { name: 'Start' });
  await expect(start).toBeFocused();
  await page.keyboard.press('Shift'); // keyboard modality: focus rings show
  await expect(start).toBeFocused();
  const ring = await start.evaluate((el) => {
    const cs = getComputedStyle(el);
    return { width: cs.outlineWidth, style: cs.outlineStyle };
  });
  expect(ring.style).toBe('solid');
  expect(ring.width).toBe('4px');
  await page.keyboard.press('Enter');
  await expect(state).toHaveText('riding');
  await expect(page.getByRole('button', { name: 'Pause' })).toBeFocused();

  // The map is behind a "Map" button on a phone.
  const mapButton = page.getByRole('button', { name: 'Map', exact: true });
  if (await mapButton.isVisible()) await mapButton.click();
  await expect(page.locator('.maplibregl-canvas')).toBeVisible();

  // The attribution "i" button (compact on every size here) and the zoom buttons are 48 px targets.
  const attrib = page.locator('.maplibregl-ctrl-attrib-button').first();
  await expect(attrib).toBeVisible();
  const box = (await attrib.boundingBox())!;
  expect(box.width, `${info.project.name} attribution width`).toBeGreaterThanOrEqual(48);
  expect(box.height, `${info.project.name} attribution height`).toBeGreaterThanOrEqual(48);

  await page.getByRole('button', { name: 'Pause' }).focus();
  await page.keyboard.press('ArrowRight');
  await expect(page.getByRole('button', { name: 'Stop', exact: true })).toBeFocused();
  await expectGoodLayout(page, 'ride (route, riding)');

  // Arrows reach the View menu; Enter opens it, Escape closes it without leaving the ride.
  const view = page.getByRole('button', { name: 'View' });
  for (let i = 0; i < 6 && !(await view.evaluate((el) => el === document.activeElement)); i++) {
    await page.keyboard.press('ArrowLeft');
  }
  await expect(view).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('radiogroup', { name: 'Ride view' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('radiogroup', { name: 'Ride view' })).toHaveCount(0);
  await expect(page).toHaveURL(/route=/);
});

test('reduced motion turns every transition off', async ({ page, routeId }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto(`/?route=${routeId}`);
  await expect(page.getByTestId('ride-state')).toBeVisible();
  const longest = await page.evaluate(() =>
    Math.max(
      ...Array.from(document.querySelectorAll('*')).flatMap((el) => {
        const cs = getComputedStyle(el);
        return [cs.transitionDuration, cs.animationDuration].flatMap((d) => d.split(',').map((x) => parseFloat(x) * (x.trim().endsWith('ms') ? 1 : 1000)));
      }),
    ),
  );
  expect(longest).toBeLessThanOrEqual(0.01);
});
