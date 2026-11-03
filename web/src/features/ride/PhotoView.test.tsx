import { act, render, screen, waitFor } from '@testing-library/react';
import { Profiler } from 'react';
import { setKioskToken, setUnauthorizedHandler } from '@/lib/api';
import type { Photo } from '@/lib/photo-select';
import { TickStore } from '@/lib/tick-store';
import { tickFixture } from '@/test-ws';
import PhotoView, { panoCrop } from './PhotoView';

const photo = (d_m: number, over: Partial<Photo> = {}): Photo => ({
  id: String(1000 + d_m),
  d_m,
  bearing_deg: 90,
  route_bearing_deg: 90,
  captured_at: Date.UTC(2024, 2, 5),
  lat: 45,
  lon: 7,
  pano: false,
  ...over,
});
const photos = [0, 15, 30, 200, 215, 230, 245, 260, 275, 290].map((d) => photo(d));

let urls: string[];
let revoked: string[];
let fetches: { url: string; headers: Headers }[];
let respond: (id: string) => Response | Promise<Response>;

beforeEach(() => {
  urls = [];
  revoked = [];
  fetches = [];
  respond = () => new Response(new Blob(['jpg']), { status: 200, headers: { 'X-Photo-Creator': 'ana' } });
  let n = 0;
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => (urls.push(`blob:p${++n}`), `blob:p${n}`), revokeObjectURL: (u: string) => void revoked.push(u) }));
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: string, init?: RequestInit) => {
      fetches.push({ url: String(input), headers: new Headers(init?.headers) });
      return respond(String(input).split('/').pop()!);
    }),
  );
  HTMLImageElement.prototype.decode = () => Promise.resolve();
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  setKioskToken(null);
  localStorage.clear();
});

const at = (store: TickStore, d: number) => act(async () => store.set(tickFixture({ distance_m: d })));
const view = () => screen.getByTestId('photo-view');
const live = () => urls.filter((u) => !revoked.includes(u));

function setup(reducedMotion = false, list = photos) {
  const store = new TickStore();
  const ui = <PhotoView photos={list} ticks={store} reducedMotion={reducedMotion} />;
  const r = render(ui);
  return { store, ...r };
}

describe('PhotoView', () => {
  it('shows the photo for the current distance, with the kiosk bearer', async () => {
    setKioskToken('wat_k');
    const { store } = setup();
    await at(store, 22);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1030'));
    const f = fetches.find((x) => x.url.endsWith('/api/mapillary/images/1030'))!;
    expect(f.headers.get('Authorization')).toBe('Bearer wat_k');
    expect(screen.queryByTestId('photo-credit')).toBeNull();
  });

  it('revokes old object URLs and keeps at most 6 alive', async () => {
    const { store, unmount } = setup();
    await at(store, 22);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1030'));
    for (const d of [200, 230, 260, 290]) {
      await at(store, d);
      await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', String(1000 + d)));
    }
    expect(revoked.length).toBeGreaterThan(0);
    expect(live().length).toBeLessThanOrEqual(6);
    unmount();
    expect(live()).toEqual([]);
  });

  it('image 502 keeps last photo and shows message', async () => {
    const { store } = setup();
    await at(store, 22);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1030'));
    respond = () => new Response('{}', { status: 502 });
    await at(store, 260); // not among the prefetched photos
    expect(await screen.findByText('Street photos unavailable right now')).toBeInTheDocument();
    expect(view()).toHaveAttribute('data-photo-id', '1030');
    // retries on the next index change
    respond = () => new Response(new Blob(['x']), { status: 200, headers: { 'X-Photo-Creator': 'bo' } });
    await at(store, 290);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1290'));
    expect(screen.queryByText('Street photos unavailable right now')).not.toBeInTheDocument();
  });

  it('says so when the stretch has no photo', async () => {
    const { store } = setup();
    await at(store, 120);
    expect(await screen.findByText('No street photo for this stretch')).toBeInTheDocument();
    expect(view()).not.toHaveAttribute('data-photo-id');
  });

  it('reduced motion sets no transition; otherwise a 600 ms opacity fade', async () => {
    const a = setup(true);
    await at(a.store, 22);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1030'));
    for (const img of view().querySelectorAll('img')) expect(img.style.transition).toBe('');
    a.unmount();
    const b = setup(false);
    await at(b.store, 22);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1030'));
    expect(Array.from(view().querySelectorAll('img')).some((i) => i.style.transition.includes('600ms'))).toBe(true);
  });

  it('re-renders only when the shown photo changes', async () => {
    let updates = 0;
    const store = new TickStore();
    render(
      <Profiler id="p" onRender={(_i, phase) => phase === 'update' && updates++}>
        <PhotoView photos={photos} ticks={store} reducedMotion />
      </Profiler>,
    );
    await at(store, 22);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1030'));
    for (let d = 22; d < 29; d += 1) await at(store, d); // same photo: no renders
    await at(store, 200);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1200'));
    expect(updates).toBeLessThanOrEqual(2);
  });

  it('a 401 on the image goes through the app unauthorized handling', async () => {
    const handler = vi.fn();
    setUnauthorizedHandler(handler);
    respond = () => new Response('{}', { status: 401 });
    const { store } = setup();
    await at(store, 22);
    await waitFor(() => expect(fetches.length).toBeGreaterThanOrEqual(4)); // the photo and its three prefetches all fail
    await waitFor(() => expect(handler).toHaveBeenCalled());
    expect(handler).toHaveBeenCalledTimes(1);
    setUnauthorizedHandler(() => {});
  });

  it('keeps the URL being decoded alive when other loads trim the cache', async () => {
    let release: () => void = () => {};
    HTMLImageElement.prototype.decode = () => new Promise<void>((r) => (release = r));
    const { store } = setup();
    await at(store, 22);
    await waitFor(() => expect(fetches.some((f) => f.url.endsWith('/1030'))).toBe(true));
    await waitFor(() => expect(fetches.length).toBeGreaterThanOrEqual(4)); // prefetch of the next three
    const first = urls[fetches.findIndex((f) => f.url.endsWith('/1030'))];
    expect(revoked).not.toContain(first);
    release();
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1030'));
  });
});

describe('pano forward crop', () => {
  it('centres on the riding direction and wraps at the seam', () => {
    const col = (cam: number, ride: number) => panoCrop({ bearing_deg: cam, route_bearing_deg: ride }).col;
    expect(col(90, 90)).toBeCloseTo(0.5);
    expect(col(90, 180)).toBeCloseTo(0.75);
    expect(col(90, 0)).toBeCloseTo(0.25);
    expect(col(350, 10)).toBeCloseTo(0.5 + 20 / 360); // across north
    expect(col(10, 350)).toBeCloseTo(0.5 - 20 / 360);
    expect(col(0, 180)).toBeCloseTo(0); // behind the camera = the seam
    expect(panoCrop({ bearing_deg: 0, route_bearing_deg: 180 }).backgroundPosition).toContain('cqw');
  });

  it('crops panos with a repeat-x background and leaves flat photos alone', async () => {
    const list = [photo(0, { pano: true, bearing_deg: 0, route_bearing_deg: 90 }), photo(100)];
    const { store } = setup(true, list);
    await at(store, 0);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1000'));
    const img = () => Array.from(view().querySelectorAll('img')).find((i) => i.getAttribute('src'))!;
    expect(img().dataset.pano).toBe('true');
    expect(img().style.backgroundRepeat).toBe('repeat-x');
    expect(img().style.backgroundImage).toContain('blob:');
    await at(store, 100);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1100'));
    expect(img().dataset.pano).toBeUndefined();
    expect(img().style.backgroundImage).toBe('');
    expect(img().style.objectFit).toBe('');
  });
});

describe('pano look-around', () => {
  const key = (k: string) => {
    const e = new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true });
    act(() => void view().dispatchEvent(e));
    return e;
  };
  const pano = [photo(0, { pano: true, bearing_deg: 0, route_bearing_deg: 90 }), photo(100)];

  it('yaw moves the crop centre and wraps at the seam', () => {
    const base = { bearing_deg: 0, route_bearing_deg: 90 };
    expect(panoCrop(base, 36).col).toBeCloseTo(0.5 + 126 / 360);
    expect(panoCrop(base, -36).col).toBeCloseTo(0.5 + 54 / 360);
    expect(panoCrop({ bearing_deg: 0, route_bearing_deg: 175 }, 10).col).toBeCloseTo(0.5 - 175 / 360); // 185 wraps to -175
    expect(panoCrop(base, 0)).toEqual(panoCrop(base));
  });

  it('a panorama is focusable, turns with arrows and eases back after 5 s', async () => {
    const { store } = setup(false, pano);
    await at(store, 0);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1000'));
    expect(view()).toHaveAttribute('tabindex', '0');
    expect(view()).toHaveAttribute('data-nav');
    expect(view().getAttribute('aria-label')).toMatch(/Left or Right arrow/);
    vi.useFakeTimers();
    expect(key('ArrowRight').defaultPrevented).toBe(true);
    expect(view().dataset.yaw).toBe('15.0');
    key('ArrowLeft');
    key('ArrowLeft');
    expect(view().dataset.yaw).toBe('-15.0');
    act(() => void vi.advanceTimersByTime(5200));
    expect(Math.abs(Number(view().dataset.yaw))).toBeLessThan(15);
    act(() => void vi.advanceTimersByTime(3000));
    expect(view().dataset.yaw).toBe('0.0');
  });

  it('reduced motion snaps back at once', async () => {
    const { store } = setup(true, pano);
    await at(store, 0);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1000'));
    vi.useFakeTimers();
    key('ArrowRight');
    act(() => void vi.advanceTimersByTime(5001));
    expect(view().dataset.yaw).toBe('0.0');
  });

  it('stays focusable on a flat photo (focus must not fall to the page) but ignores arrows', async () => {
    const { store } = setup(true, pano);
    await at(store, 100);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1100'));
    expect(view()).toHaveAttribute('tabindex', '0');
    expect(view()).toHaveAttribute('data-nav');
    expect(key('ArrowRight').defaultPrevented).toBe(false);
  });

  it('lookable=false (small inset) is not a focus stop and takes no input', async () => {
    const store = new TickStore();
    render(<PhotoView photos={pano} ticks={store} reducedMotion lookable={false} />);
    await at(store, 0);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1000'));
    expect(view()).not.toHaveAttribute('tabindex');
    expect(view()).not.toHaveAttribute('data-nav');
    expect(key('ArrowRight').defaultPrevented).toBe(false);
    expect(view().dataset.yaw).toBeUndefined();
  });

  it('a new photo looks forward, and a flat photo ignores the arrows', async () => {
    const { store } = setup(true, pano);
    await at(store, 0);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1000'));
    key('ArrowRight');
    expect(view().dataset.yaw).toBe('15.0');
    await at(store, 100);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1100'));
    expect(view().dataset.yaw).toBe('0.0');
    expect(key('ArrowRight').defaultPrevented).toBe(false);
    expect(view().dataset.yaw).toBe('0.0');
  });
});
