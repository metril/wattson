import { render, screen, waitFor } from '@testing-library/react';
import MapView, { STYLE_URL, type MapApi } from './MapView';

const h = vi.hoisted(() => {
  const state = {
    maps: [] as FakeMap[],
    markers: [] as FakeMarker[],
    fail: false,
  };
  class FakeMap {
    opts: Record<string, unknown>;
    handlers: Record<string, ((e?: unknown) => void)[]> = {};
    sources: Record<string, { data: unknown; setData(d: unknown): void }> = {};
    layers = new Set<string>();
    removed = false;
    constructor(opts: Record<string, unknown>) {
      if (state.fail) throw new Error('webgl');
      this.opts = opts;
      state.maps.push(this);
    }
    on(ev: string, fn: (e?: unknown) => void) {
      (this.handlers[ev] ??= []).push(fn);
    }
    emit(ev: string, e?: unknown) {
      this.handlers[ev]?.forEach((f) => f(e));
    }
    controls: unknown[] = [];
    addControl(c: unknown) {
      this.controls.push(c);
      // The real NavigationControl puts its compass button inside the map container.
      const b = document.createElement('button');
      b.className = 'maplibregl-ctrl-compass';
      (this.opts.container as HTMLElement).append(b);
    }
    easeTo = vi.fn();
    resize = vi.fn();
    addSource(id: string, s: { data: unknown }) {
      this.sources[id] = { data: s.data, setData: (d) => (this.sources[id].data = d) };
    }
    addLayer(l: { id: string }) {
      this.layers.add(l.id);
    }
    getSource(id: string) {
      return this.sources[id];
    }
    getLayer(id: string) {
      return this.layers.has(id) ? {} : undefined;
    }
    queryRenderedFeatures() {
      return [{ properties: { name: 'E5_N45' } }];
    }
    getCenter() {
      return { lng: 8.25, lat: 46.5 };
    }
    fitBounds = vi.fn();
    setStyle = vi.fn();
    remove() {
      this.removed = true;
    }
  }
  class FakeMarker {
    ll: unknown = null;
    removed = false;
    constructor() {
      state.markers.push(this);
    }
    setLngLat(ll: unknown) {
      this.ll = ll;
      return this;
    }
    addTo() {
      return this;
    }
    remove() {
      this.removed = true;
    }
  }
  class NavigationControl {
    constructor(public opts: Record<string, unknown>) {}
  }
  return { state, FakeMap, FakeMarker, NavigationControl };
});

vi.mock('@/lib/maplibre', () => ({ loadMapLibre: async () => ({ Map: h.FakeMap, Marker: h.FakeMarker, NavigationControl: h.NavigationControl }) }));

beforeEach(() => {
  h.state.maps.length = 0;
  h.state.markers.length = 0;
  h.state.fail = false;
});

async function mount(props: Partial<React.ComponentProps<typeof MapView>> = {}) {
  let api!: MapApi;
  const r = render(<MapView onReady={(a) => (api = a)} {...props} />);
  await waitFor(() => expect(api).toBeDefined());
  return { api, map: h.state.maps[0], ...r };
}

describe('MapView compass and heading-up camera', () => {
  it('shows the compass control', async () => {
    const { map } = await mount();
    expect((map.controls[0] as { opts: { showCompass: boolean } }).opts.showCompass).toBe(true);
  });

  it('pressing the compass reports it', async () => {
    const onCompass = vi.fn();
    const { container } = await mount({ onCompass });
    (container.querySelector('.maplibregl-ctrl-compass') as HTMLElement).click();
    expect(onCompass).toHaveBeenCalledTimes(1);
  });

  it('follow centres on the rider with the bearing; zoom only when given; no animation when asked', async () => {
    const { api, map } = await mount();
    api.follow([7, 45], 120, { zoom: 15 });
    expect(map.easeTo).toHaveBeenLastCalledWith(expect.objectContaining({ center: [7, 45], bearing: 120, zoom: 15, duration: 900 }));
    api.follow([7, 46], 121, { animate: false });
    const o = map.easeTo.mock.calls.at(-1)![0];
    expect(o).toMatchObject({ bearing: 121, duration: 0 });
    expect(o).not.toHaveProperty('zoom');
  });

  it('fit always ends north up', async () => {
    const { api, map } = await mount();
    api.fit([0, 0, 1, 1]);
    expect(map.fitBounds.mock.calls[0][1]).toMatchObject({ bearing: 0 });
  });
});

describe('MapView', () => {
  it('builds the map on the OpenFreeMap style', async () => {
    const { map } = await mount();
    expect(map.opts.style).toBe(STYLE_URL);
  });

  it('forwards map clicks as [lon, lat]', async () => {
    const onClick = vi.fn();
    const { map } = await mount({ onClick });
    map.emit('click', { lngLat: { lng: 7.5, lat: 45.5 }, point: { x: 1, y: 1 } });
    expect(onClick).toHaveBeenCalledWith([7.5, 45.5]);
  });

  it('reports the map centre as [lon, lat]', async () => {
    const { api } = await mount();
    expect(api.getCenter()).toEqual([8.25, 46.5]);
  });

  it('places and clears the marker', async () => {
    const { api } = await mount();
    api.setMarker([1, 2]);
    expect(h.state.markers[0].ll).toEqual([1, 2]);
    api.setMarker([3, 4]);
    expect(h.state.markers[0].ll).toEqual([3, 4]);
    api.setMarker(null);
    expect(h.state.markers[0].removed).toBe(true);
  });

  it('applies a line set before the style loads', async () => {
    const { api, map } = await mount();
    const line = { type: 'LineString' as const, coordinates: [[0, 0], [1, 1]] };
    api.setLine(line);
    map.emit('style.load');
    const data = map.sources.route.data as { features: { geometry: unknown }[] };
    expect(data.features[0].geometry).toEqual(line);
    api.setLine(null);
    expect((map.sources.route.data as { features: unknown[] }).features).toHaveLength(0);
  });

  it('routes a tile click to the tile handler instead of the map click', async () => {
    const onClick = vi.fn();
    const onTile = vi.fn();
    const { api, map } = await mount({ onClick });
    map.emit('style.load');
    api.setTiles({ type: 'FeatureCollection', features: [] }, onTile);
    map.emit('click', { lngLat: { lng: 1, lat: 1 }, point: { x: 0, y: 0 } });
    expect(onTile).toHaveBeenCalledWith('E5_N45');
    expect(onClick).not.toHaveBeenCalled();
  });

  it('falls back to a blank style when the hosted style fails', async () => {
    const { map } = await mount();
    map.emit('error');
    expect(map.setStyle).toHaveBeenCalledTimes(1);
    map.emit('error');
    expect(map.setStyle).toHaveBeenCalledTimes(1);
  });

  it('warns about MapLibre errors once the style is ready', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { map } = await mount();
    map.emit('style.load');
    map.emit('error', { error: new Error('tile 404') });
    expect(warn).toHaveBeenCalledTimes(1);
    expect(map.setStyle).not.toHaveBeenCalled();
    warn.mockRestore();
  });

  it('removes the map on unmount', async () => {
    const { map, unmount } = await mount();
    unmount();
    expect(map.removed).toBe(true);
  });

  it('shows "Map unavailable" when WebGL fails', async () => {
    h.state.fail = true;
    render(<MapView onReady={() => {}} />);
    expect(await screen.findByText('Map unavailable')).toBeInTheDocument();
  });
});
