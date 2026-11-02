import { act, render, waitFor } from '@testing-library/react';
import { Profiler } from 'react';
import { positionAt } from '@/lib/route-math';
import { TickStore } from '@/lib/tick-store';
import { loadPrefs } from '@/lib/view-prefs';
import { profileFixture } from '@/test-utils';
import { tickFixture } from '@/test-ws';
import Ride3D, { DEM_URL } from './Ride3D';

const h = vi.hoisted(() => {
  class FakeMarker {
    ll: [number, number] | null = null;
    removed = false;
    constructor(public opts: unknown) {}
    setLngLat(ll: [number, number]) {
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
  class FakeMap {
    handlers: Record<string, ((e?: unknown) => void)[]> = {};
    sources: Record<string, Record<string, unknown>> = {};
    layers: string[] = [];
    hidden: string[] = [];
    removed = false;
    terrain: unknown[] = [];
    jumps: { center: [number, number]; bearing: number }[] = [];
    constructor(public opts: Record<string, unknown>) {
      if (state.fail) throw new Error('webgl');
      state.maps.push(this);
    }
    on(ev: string, fn: (e?: unknown) => void) {
      (this.handlers[ev] ??= []).push(fn);
    }
    emit(ev: string) {
      this.handlers[ev]?.forEach((f) => f());
    }
    addSource(id: string, s: Record<string, unknown>) {
      this.sources[id] = s;
    }
    addLayer(l: { id: string }) {
      this.layers.push(l.id);
    }
    setTerrain(t: unknown) {
      this.terrain.push(t);
    }
    getStyle() {
      return {
        layers: [
          { id: 'bld', type: 'fill-extrusion' },
          { id: 'poi', type: 'symbol', minzoom: 16 },
          { id: 'road-name', type: 'symbol', minzoom: 12 },
          { id: 'water', type: 'fill' },
        ],
      };
    }
    setLayoutProperty(id: string) {
      this.hidden.push(id);
    }
    setStyle = vi.fn();
    getPitch() {
      return 62;
    }
    jumpTo(o: { center: [number, number]; bearing: number }) {
      this.jumps.push(o);
    }
    remove() {
      this.removed = true;
    }
  }
  const state = { maps: [] as FakeMap[], fail: false };
  return { state, FakeMap, FakeMarker };
});
vi.mock('maplibre-gl', () => ({ Map: h.FakeMap, Marker: h.FakeMarker }));
vi.mock('@/features/map/MapView', () => ({ STYLE_URL: 'style-url' }));

// Manual rAF: step(ms) advances the clock and runs the queued callbacks once.
let queue: ((t: number) => void)[] = [];
let clock = 1000;
const step = (ms = 16) => {
  clock += ms;
  const q = queue;
  queue = [];
  act(() => q.forEach((f) => f(clock)));
};

const profile = profileFixture({ length_m: 900, step_m: 450, lon: [7, 7.05, 7.1], lat: [45, 45.05, 45.1] });
const line = { type: 'LineString' as const, coordinates: [[7, 45], [7.1, 45.1]] };

function setup(over: Partial<React.ComponentProps<typeof Ride3D>> = {}) {
  const ticks = new TickStore();
  const onFallback = vi.fn();
  const ui = (
    <Ride3D profile={profile} line={line} ticks={ticks} exaggeration={1.5} tier="full" onFallback={onFallback} {...over} />
  );
  const r = render(ui);
  return { ticks, onFallback, ...r };
}
const ready = async (i = 0) => {
  await waitFor(() => expect(h.state.maps.length).toBeGreaterThan(i));
  act(() => h.state.maps[i].emit('style.load'));
  return h.state.maps[i];
};

beforeEach(() => {
  h.state.maps = [];
  h.state.fail = false;
  queue = [];
  clock = 1000;
  vi.stubGlobal('requestAnimationFrame', (f: (t: number) => void) => queue.push(f));
  vi.stubGlobal('cancelAnimationFrame', () => {
    queue = [];
  });
  vi.stubGlobal('devicePixelRatio', 3);
});
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('Ride3D', () => {
  it('adds the terrarium DEM, terrain and the route on load', async () => {
    setup();
    const m = await ready();
    expect(m.sources.terrain).toMatchObject({
      type: 'raster-dem',
      encoding: 'terrarium',
      tiles: [DEM_URL],
      tileSize: 256,
      maxzoom: 13,
    });
    expect(m.terrain).toContainEqual({ source: 'terrain', exaggeration: 1.5 });
    expect(m.layers).toEqual(['route-casing', 'route-line']);
    expect(m.hidden).toEqual([]);
    expect(m.opts).toMatchObject({ pitch: 62, maxPitch: 70, zoom: 15.5, interactive: false, fadeDuration: 0, pixelRatio: 1.5 });
  });

  it('lite tier: lower DEM zoom, pixel ratio 1, heavy layers hidden', async () => {
    setup({ tier: 'lite' });
    const m = await ready();
    expect(m.sources.terrain).toMatchObject({ maxzoom: 11 });
    expect(m.opts.pixelRatio).toBe(1);
    expect(m.hidden).toEqual(['bld', 'poi']);
  });

  it('a tick moves the camera and marker in the next frame', async () => {
    const { ticks, container } = setup();
    const m = await ready();
    act(() => ticks.set(tickFixture({ distance_m: 15, speed_kmh: 0 })));
    step();
    expect(m.jumps.at(-1)?.center).toEqual(positionAt(profile, 15));
    const el = container.querySelector('[data-testid="route-3d"]') as HTMLElement;
    expect(el.dataset.pitch).toBe('62.0');
    expect(el.dataset.bearing).toMatch(/^\d+\.\d$/);
  });

  it('extrapolates between ticks at the current speed', async () => {
    const { ticks } = setup();
    const m = await ready();
    act(() => ticks.set(tickFixture({ distance_m: 100, speed_kmh: 36 })));
    step();
    step(500);
    expect(m.jumps.at(-1)?.center).toEqual(positionAt(profile, 105));
  });

  it('renders once however many ticks arrive', async () => {
    const renders = vi.fn();
    const ticks = new TickStore();
    render(
      <Profiler id="r" onRender={renders}>
        <Ride3D profile={profile} line={line} ticks={ticks} exaggeration={1.5} tier="full" onFallback={vi.fn()} />
      </Profiler>,
    );
    await ready();
    for (let i = 0; i < 20; i++) {
      act(() => ticks.set(tickFixture({ distance_m: i * 5 })));
      step();
    }
    expect(renders).toHaveBeenCalledTimes(1);
  });

  it('webglcontextlost triggers fallback', async () => {
    const { onFallback } = setup();
    const m = await ready();
    act(() => m.emit('webglcontextlost'));
    expect(onFallback).toHaveBeenCalledWith('context_lost');
  });

  it('a throwing constructor falls back with webgl', async () => {
    h.state.fail = true;
    const { onFallback } = setup();
    await waitFor(() => expect(onFallback).toHaveBeenCalledWith('webgl'));
  });

  it('slow frames: first drop to lite with a new map, then fall back to 2D', async () => {
    const { onFallback } = setup();
    await ready();
    for (let i = 0; i < 400 && h.state.maps.length < 2; i++) step(50);
    expect(h.state.maps).toHaveLength(2);
    expect(h.state.maps[0].removed).toBe(true);
    expect(loadPrefs().tier).toBe('lite');
    expect(onFallback).not.toHaveBeenCalled();
    const lite = await ready(1);
    expect(lite.sources.terrain).toMatchObject({ maxzoom: 11 });
    for (let i = 0; i < 500 && !onFallback.mock.calls.length; i++) step(50);
    expect(onFallback).toHaveBeenCalledWith('slow');
    expect(onFallback).toHaveBeenCalledTimes(1);
  });

  it('terrain height changes re-apply the terrain', async () => {
    const { rerender, ticks, onFallback } = setup();
    const m = await ready();
    rerender(<Ride3D profile={profile} line={line} ticks={ticks} exaggeration={2.5} tier="full" onFallback={onFallback} />);
    expect(m.terrain.at(-1)).toEqual({ source: 'terrain', exaggeration: 2.5 });
    expect(h.state.maps).toHaveLength(1);
  });

  it('removes the map on unmount', async () => {
    const { unmount } = setup();
    const m = await ready();
    unmount();
    expect(m.removed).toBe(true);
  });
});
