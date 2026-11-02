import { Profiler } from 'react';
import { act, render, screen } from '@testing-library/react';
import { MetricGrid } from './MetricGrid';
import { TickStore } from '@/lib/tick-store';
import { tickFixture } from '@/test-ws';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('MetricGrid', () => {
  it('formats ticks without re-rendering', () => {
    const ticks = new TickStore();
    let renders = 0;
    render(
      <Profiler id="g" onRender={() => renders++}>
        <MetricGrid ticks={ticks} />
      </Profiler>,
    );
    expect(renders).toBe(1);
    act(() => {
      ticks.set(
        tickFixture({
          power_w: 129.6,
          cadence_rpm: 80,
          speed_kmh: 31.24,
          distance_m: 2761,
          elapsed_s: 65,
          level: 5,
          reported_level: 0,
        }),
      );
      vi.runAllTimers();
    });
    for (const t of ['130', '80', '31.2', '2.76', '00:01:05', 'L5 (–)']) {
      expect(screen.getByText(t)).toBeInTheDocument();
    }
    act(() => {
      ticks.set(tickFixture({ level: 6, reported_level: 6, power_w: 200 }));
      vi.runAllTimers();
    });
    expect(screen.getByText('L6 (6)')).toBeInTheDocument();
    expect(renders).toBe(1);
  });

  it('can hide the level tile and host the level controls instead', () => {
    render(
      <MetricGrid ticks={new TickStore()} showLevel={false}>
        <div>controls</div>
      </MetricGrid>,
    );
    expect(screen.getByText('controls')).toBeInTheDocument();
    expect(screen.queryByText('Level')).not.toBeInTheDocument();
  });
});
