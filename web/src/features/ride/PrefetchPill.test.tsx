import { act, screen, waitFor } from '@testing-library/react';
import { profileFixture, renderWithClient, stubApi } from '@/test-utils';
import { PrefetchPill } from './PrefetchPill';

const h = vi.hoisted(() => ({
  prefetchRoute: vi.fn(),
  hasController: vi.fn(() => true),
}));
vi.mock('@/sw/register', () => ({ prefetchRoute: h.prefetchRoute, hasController: h.hasController }));
vi.mock('@/sw/corridor', async (orig) => ({
  ...(await orig<typeof import('@/sw/corridor')>()),
  vectorTemplate: async () => 'https://tiles.openfreemap.org/planet/v/{z}/{x}/{y}.pbf',
}));

const route = (source = 'gpx') => ({ id: 7, name: 'Loop', source, geojson: {}, bbox: [7, 45, 7.1, 45.1], length_m: 1000 });
const api = (source?: string) =>
  stubApi({
    'GET /api/routes/7': [200, route(source)],
    'GET /api/routes/7/profile': [200, profileFixture({ route_id: 7 })],
  });

beforeEach(() => {
  h.prefetchRoute.mockReset();
  h.hasController.mockReturnValue(true);
  localStorage.clear();
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  Object.defineProperty(navigator, 'connection', { value: undefined, configurable: true });
});

describe('PrefetchPill', () => {
  it('shows progress then the saved message, and hides it after 5 s', async () => {
    let progress: (d: number, t: number) => void = () => {};
    let done: (v: { cached: number; failed: number }) => void = () => {};
    h.prefetchRoute.mockImplementation((_u: string[], p: typeof progress) => {
      progress = p;
      return new Promise((r) => (done = r));
    });
    api();
    renderWithClient(<PrefetchPill routeId={7} />);
    await waitFor(() => expect(h.prefetchRoute).toHaveBeenCalled());
    expect(h.prefetchRoute.mock.calls[0][0].length).toBeGreaterThan(0);
    act(() => progress(42, 100));
    expect(await screen.findByRole('status')).toHaveTextContent('Saving map for offline… 42 %');
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await act(async () => done({ cached: 100, failed: 0 }));
    expect(screen.getByRole('status')).toHaveTextContent('Map saved for this route');
    expect(localStorage.getItem('wattson.prefetched.7')).toBeTruthy();
    await act(async () => void vi.advanceTimersByTime(5100));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('says storage is full on a quota error', async () => {
    h.prefetchRoute.mockRejectedValue(new Error('quota'));
    api();
    renderWithClient(<PrefetchPill routeId={7} />);
    expect(await screen.findByRole('status')).toHaveTextContent("Couldn't save the map: storage is full");
    expect(localStorage.getItem('wattson.prefetched.7')).toBeNull();
  });

  it('stays quiet on other errors', async () => {
    h.prefetchRoute.mockRejectedValue(new Error('boom'));
    api();
    renderWithClient(<PrefetchPill routeId={7} />);
    await waitFor(() => expect(h.prefetchRoute).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('skips a route saved 2 days ago but not one saved 8 days ago', async () => {
    localStorage.setItem('wattson.prefetched.7', new Date(Date.now() - 2 * 864e5).toISOString());
    api();
    const { unmount } = renderWithClient(<PrefetchPill routeId={7} />);
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(2));
    await act(async () => {});
    expect(h.prefetchRoute).not.toHaveBeenCalled();
    unmount();
    localStorage.setItem('wattson.prefetched.7', new Date(Date.now() - 8 * 864e5).toISOString());
    h.prefetchRoute.mockResolvedValue({ cached: 1, failed: 0 });
    api();
    renderWithClient(<PrefetchPill routeId={7} />);
    await waitFor(() => expect(h.prefetchRoute).toHaveBeenCalled());
  });

  it('skips without a controller, with Save-Data, and for synthetic routes', async () => {
    h.hasController.mockReturnValue(false);
    api();
    const a = renderWithClient(<PrefetchPill routeId={7} />);
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(2));
    await act(async () => {});
    a.unmount();
    h.hasController.mockReturnValue(true);
    Object.defineProperty(navigator, 'connection', { value: { saveData: true }, configurable: true });
    api();
    const b = renderWithClient(<PrefetchPill routeId={7} />);
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(2));
    await act(async () => {});
    b.unmount();
    Object.defineProperty(navigator, 'connection', { value: undefined, configurable: true });
    api('synthetic');
    renderWithClient(<PrefetchPill routeId={7} />);
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(2));
    await act(async () => {});
    expect(h.prefetchRoute).not.toHaveBeenCalled();
  });
});
