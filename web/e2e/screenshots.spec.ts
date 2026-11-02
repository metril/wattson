import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import type { Locator, Page } from '@playwright/test';
import { apiAs, expect, importRamp, setupAdmin, test as base, useFakeMapillary, type Cookie } from './fixtures';

// The user guide's pictures (docs/images). Not part of the suite: `npm run screenshots` after `make build`.
// Real OpenFreeMap and Terrarium tiles (needs internet); photos come from the worker's fake Mapillary.
const OUT = resolve(import.meta.dirname, '../../docs/images');

// A climb above a lake (Alps) that looks good under real tiles: a swinging line with ~700 m of ascent over 5 km.
const SCENIC_GPX = `<?xml version="1.0"?><gpx version="1.1" creator="wattson-docs" xmlns="http://www.topografix.com/GPX/1/1"><trk><name>Lakeside climb</name><trkseg>${Array.from(
  { length: 61 },
  (_, i) => {
    const t = i / 60;
    return `<trkpt lat="${(45.8394 - 0.0361 * t).toFixed(6)}" lon="${(6.2330 + 0.0017 * t + 0.0045 * Math.sin(t * Math.PI * 4)).toFixed(6)}"><ele>${(460 + 700 * t).toFixed(1)}</ele></trkpt>`;
  },
).join('')}</trkseg></trk></gpx>`;

async function importScenic(baseURL: string, cookies: Cookie[]): Promise<number> {
  const fd = new FormData();
  fd.append('file', new Blob([SCENIC_GPX]), 'lakeside.gpx');
  const res = await fetch(`${baseURL}/api/routes/import`, {
    method: 'POST',
    headers: { cookie: cookies.map((c) => `${c.name}=${c.value}`).join('; ') },
    body: fd,
  });
  if (!res.ok) throw new Error(`import failed: ${res.status} ${await res.text()}`);
  return (await res.json()).id;
}

// `routeId` is the scenic climb; `rampId` is the short ramp the fake Mapillary has photos for.
const test = base.extend<{ routeId: number; rampId: number }>({
  // After routeId, which signs in and sets everything up.
  rampId: async ({ baseURL, context, routeId }, use) => {
    void routeId;
    const cookies = (await context.cookies()).map((c) => ({ name: c.name, value: c.value, url: baseURL }));
    await use(await importRamp(baseURL, cookies));
  },
  routeId: async ({ context, baseURL, fakeMapillary, fakeBRouter, segmentsDir }, use, info) => {
    const cookies = await setupAdmin(baseURL);
    await useFakeMapillary(baseURL, cookies, fakeMapillary);
    await apiAs(baseURL, cookies, 'PATCH', '/api/settings', { brouter: { url: fakeBRouter.url, segments_dir: segmentsDir } });
    await context.addCookies(cookies);
    mkdirSync(OUT, { recursive: true });
    info.setTimeout(120_000);
    await use(await importScenic(baseURL, cookies));
  },
});

/** A road scene to stand in for a street photo (the committed fixture is a 64x48 blue block). */
async function scenePhoto(page: Page): Promise<Buffer> {
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.setContent(`<body style="margin:0"><svg xmlns="http://www.w3.org/2000/svg" width="1280" height="720" viewBox="0 0 1280 720">
    <defs><linearGradient id="sky" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#6aa6d8"/><stop offset="1" stop-color="#d9ecf7"/></linearGradient></defs>
    <rect width="1280" height="720" fill="url(#sky)"/>
    <path d="M0 330 L220 250 L430 320 L640 230 L860 310 L1070 240 L1280 320 V420 H0Z" fill="#7d9aa8"/>
    <rect y="400" width="1280" height="320" fill="#6f9a4d"/>
    <path d="M520 400 L760 400 L1180 720 L100 720Z" fill="#555a60"/>
    <path d="M636 400 L644 400 L690 720 L590 720Z" fill="#e8e2c0"/>
    <g fill="#2f5d2a"><circle cx="140" cy="360" r="70"/><circle cx="260" cy="380" r="55"/><circle cx="1060" cy="350" r="75"/><circle cx="1180" cy="380" r="60"/></g>
  </svg></body>`);
  return page.screenshot({ type: 'jpeg', quality: 82 });
}

/** Connects the simulator and rides the route for a few seconds. */
async function ride(page: Page, routeId: number) {
  await page.goto(`/?route=${routeId}`);
  await page.getByRole('button', { name: 'Connect' }).click();
  await expect(page.getByTestId('ride-state')).toHaveText('ready', { timeout: 5_000 });
  await page.getByRole('button', { name: 'Start' }).click();
  await expect(page.getByTestId('ride-state')).toHaveText('riding');
}

async function settle(page: Page) {
  await page.waitForLoadState('networkidle');
  await page.waitForTimeout(3_000);
}

async function shoot(page: Page, name: string, mask: Locator[] = []) {
  await page.screenshot({ path: resolve(OUT, `${name}.png`), animations: 'disabled', mask, maskColor: '#9aa5a0' });
}

async function setView(page: Page, view: '2d' | '3d' | 'photo') {
  await page.addInitScript((v) => localStorage.setItem('wattson.view', v), view);
}

test('ride-tv-3d: the TV with the 3D flyover', async ({ page, routeId }) => {
  // Software rendering is far slower than a TV's GPU: report a steady 60 fps to the flyover's frame-rate
  // check (it reads the rAF timestamp) so the picture is not taken after a fallback to 2D.
  await page.addInitScript(() => {
    const raf = window.requestAnimationFrame.bind(window);
    let t = 0;
    window.requestAnimationFrame = (cb) => raf(() => cb((t += 1000 / 60)));
  });
  await page.setViewportSize({ width: 1920, height: 1080 });
  await setView(page, '3d');
  await ride(page, routeId);
  await expect(page.getByTestId('route-3d')).toBeVisible();
  await settle(page);
  await page.waitForTimeout(10_000); // software rendering draws the tiles a few at a time
  const view = page.getByTestId('route-3d');
  await expect(view).toBeVisible(); // still 3D: no fallback to the 2D map
  await expect.poll(async () => Number((await view.getAttribute('data-pitch')) ?? 0)).toBeGreaterThan(50);
  await shoot(page, 'ride-tv-3d');
});

test('ride-tablet-2d: the tablet with the map and profile', async ({ page, routeId }) => {
  await page.setViewportSize({ width: 1024, height: 768 });
  await setView(page, '2d');
  await ride(page, routeId);
  await expect(page.locator('.maplibregl-canvas')).toBeVisible();
  await settle(page);
  await shoot(page, 'ride-tablet-2d');
});

test('ride-phone: the phone, portrait', async ({ page, routeId }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await setView(page, '2d');
  await ride(page, routeId);
  await page.getByRole('button', { name: 'Map', exact: true }).click();
  await expect(page.locator('.maplibregl-canvas')).toBeVisible();
  await settle(page);
  await shoot(page, 'ride-phone');
});

test('ride-tablet-photos: street photos on the tablet', async ({ page, rampId, fakeMapillary }) => {
  fakeMapillary.image = await scenePhoto(page);
  await page.setViewportSize({ width: 1024, height: 768 });
  await setView(page, 'photo');
  await ride(page, rampId);
  await expect(page.getByTestId('photo-view')).toHaveAttribute('data-photo-id', /^\d+$/, { timeout: 20_000 });
  await settle(page);
  await shoot(page, 'ride-tablet-photos');
});

test('routes-draw: drawing a route', async ({ page, routeId }) => {
  void routeId;
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto('/routes/new/draw?c=7.5,45.5&z=13');
  const map = page.getByTestId('draw-map');
  await expect(map).toBeVisible();
  await page.waitForLoadState('networkidle'); // clicks before the style has loaded are ignored
  await page.waitForTimeout(1_000);
  const box = (await map.boundingBox())!;
  await map.click({ position: { x: box.width * 0.3, y: box.height * 0.4 } });
  await expect(page.getByText('1 / 30 waypoints')).toBeVisible();
  await map.click({ position: { x: box.width * 0.6, y: box.height * 0.6 } });
  await expect(page.getByText('2 / 30 waypoints')).toBeVisible();
  await expect(page.getByText('Distance', { exact: true })).toBeVisible({ timeout: 10_000 });
  await settle(page);
  await shoot(page, 'routes-draw');
});

test('settings-integrations: the Street imagery card', async ({ page, routeId }) => {
  void routeId;
  await page.setViewportSize({ width: 1024, height: 900 });
  await page.goto('/settings/integrations');
  const token = page.getByRole('textbox', { name: 'Mapillary access token' });
  await expect(token).toBeVisible();
  await token.scrollIntoViewIfNeeded();
  await settle(page);
  await shoot(page, 'settings-integrations');
});

test('kiosk-pair: pairing a TV with a QR code', async ({ page, routeId }) => {
  void routeId;
  await page.setViewportSize({ width: 1024, height: 900 });
  await page.goto('/settings/tokens');
  await page.getByRole('button', { name: 'Pair a display' }).click();
  await expect(page.getByAltText('Pairing QR code')).toBeVisible();
  await settle(page);
  // The pairing token is a credential (this server is thrown away, but the picture is published): hide it and its QR code.
  await shoot(page, 'kiosk-pair', [page.getByAltText('Pairing QR code'), page.getByText(/kiosk#token=/)]);
});
