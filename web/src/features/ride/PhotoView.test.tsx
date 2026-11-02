import { act, render, screen, waitFor } from '@testing-library/react';
import { Profiler } from 'react';
import { setKioskToken, setUnauthorizedHandler } from '@/lib/api';
import type { Photo } from '@/lib/photo-select';
import { TickStore } from '@/lib/tick-store';
import { tickFixture } from '@/test-ws';
import PhotoView from './PhotoView';

const photo = (d_m: number, over: Partial<Photo> = {}): Photo => ({
  id: String(1000 + d_m),
  d_m,
  bearing_deg: 90,
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
  const ui = <PhotoView routeId={7} photos={list} ticks={store} reducedMotion={reducedMotion} />;
  const r = render(ui);
  return { store, ...r };
}

describe('PhotoView', () => {
  it('shows the photo for the current distance, with the kiosk bearer and attribution', async () => {
    setKioskToken('wat_k');
    const { store } = setup();
    await at(store, 22);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1030'));
    const f = fetches.find((x) => x.url.endsWith('/api/mapillary/images/1030'))!;
    expect(f.headers.get('Authorization')).toBe('Bearer wat_k');
    expect(screen.getByText(/Photo © ana on/)).toHaveTextContent('Photo © ana on Mapillary, CC BY-SA 4.0 · Mar 2024');
    expect(screen.getByRole('link', { name: 'Mapillary' })).toHaveAttribute('href', 'https://www.mapillary.com/app/?pKey=1030');
  });

  it('credits Mapillary contributors when the creator is unknown', async () => {
    respond = () => new Response(new Blob(['x']), { status: 200 });
    const { store } = setup();
    await at(store, 3);
    await waitFor(() => expect(view()).toHaveAttribute('data-photo-id', '1000'));
    expect(screen.getByText(/Photo © Mapillary contributors on/)).toBeInTheDocument();
  });

  it('decodes a percent-encoded creator', async () => {
    respond = () => new Response(new Blob(['x']), { status: 200, headers: { 'X-Photo-Creator': 'Jos%C3%A9' } });
    const { store } = setup();
    await at(store, 3);
    await waitFor(() => expect(screen.getByText(/Photo © José on/)).toBeInTheDocument());
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
    expect(screen.getByText(/Photo © ana on/)).toBeInTheDocument();
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
        <PhotoView routeId={7} photos={photos} ticks={store} reducedMotion />
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
    await waitFor(() => expect(handler).toHaveBeenCalled());
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
