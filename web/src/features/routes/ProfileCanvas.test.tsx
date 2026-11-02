import { act, render, screen, waitFor } from '@testing-library/react';
import { ProfileCanvas, gradeColor } from './ProfileCanvas';
import { TickStore } from '@/lib/tick-store';
import { profileFixture } from '@/test-utils';
import type { Tick } from '@/lib/ws-types';

beforeAll(() => {
  // jsdom has no layout; give elements a width so the canvas has something to draw into.
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, value: 400 });
});
afterAll(() => {
  delete (HTMLElement.prototype as unknown as Record<string, unknown>).clientWidth;
});

describe('ProfileCanvas', () => {
  it('moves the ride cursor from the tick store without re-rendering', async () => {
    const store = new TickStore();
    render(<ProfileCanvas profile={profileFixture()} cursor={store} />);
    const pos = screen.getByTestId('route-pos');
    act(() => store.set({ distance_m: 12.4 } as Tick));
    await waitFor(() => expect(pos).toHaveAttribute('data-distance-m', '12'));
    act(() => store.set({ distance_m: 18 } as Tick));
    await waitFor(() => expect(pos).toHaveAttribute('data-distance-m', '18'));
  });

  it('has no cursor without a store', () => {
    render(<ProfileCanvas profile={profileFixture()} />);
    expect(screen.queryByTestId('route-pos')).not.toBeInTheDocument();
  });

  it('describes the profile for assistive tech', () => {
    render(<ProfileCanvas profile={profileFixture()} />);
    expect(screen.getByRole('img')).toHaveAccessibleName(/Elevation profile, 0\.0 km/);
  });
});

describe('ProfileCanvas fill', () => {
  it('fills one path for a whole same-colour run', () => {
    const fill = vi.fn();
    const ctx = new Proxy({} as Record<string, unknown>, {
      get: (t, k) => (k === 'fill' ? fill : k in t ? t[k as string] : () => {}),
      set: (t, k, v) => ((t[k as string] = v), true),
    });
    const spy = vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(ctx as unknown as CanvasRenderingContext2D);
    render(<ProfileCanvas profile={profileFixture({ length_m: 200, lon: Array(21).fill(7), lat: Array(21).fill(45), ele_m: Array(21).fill(100), grade_pct: Array(21).fill(4) })} />);
    expect(fill).toHaveBeenCalledTimes(1);
    spy.mockRestore();
  });
});

describe('gradeColor', () => {
  it('bands by grade', () => {
    const c = [-1, 0, 2, 3, 6, 7].map(gradeColor);
    expect(c[1]).toBe(c[2]);
    expect(c[3]).toBe(c[4]);
    expect(new Set([c[0], c[1], c[3], c[5]]).size).toBe(4);
  });
});
