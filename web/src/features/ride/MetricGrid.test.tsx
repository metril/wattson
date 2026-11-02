import { Profiler } from 'react';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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

  it('route mode adds grade, ahead and remaining tiles without re-rendering', () => {
    const ticks = new TickStore();
    let renders = 0;
    render(
      <Profiler id="g" onRender={() => renders++}>
        <MetricGrid ticks={ticks} route />
      </Profiler>,
    );
    act(() => {
      ticks.set(
        tickFixture({ route_id: 3, route_length_m: 900, distance_m: 300, grade_pct: 3.26, grade_ahead_pct: 6.04 }),
      );
      vi.runAllTimers();
    });
    expect(screen.getByText('3.3')).toBeInTheDocument();
    expect(screen.getByText('6.0')).toBeInTheDocument();
    expect(screen.getByText('0.60')).toBeInTheDocument();
    expect(screen.getByText('Remaining')).toBeInTheDocument();
    expect(renders).toBe(1);
  });

  it('free ride has no route tiles', () => {
    render(<MetricGrid ticks={new TickStore()} />);
    expect(screen.queryByText('Remaining')).not.toBeInTheDocument();
  });

  it('shows heart rate through the DOM, a dash for 0, without re-rendering', () => {
    const ticks = new TickStore();
    let renders = 0;
    render(
      <Profiler id="g" onRender={() => renders++}>
        <MetricGrid ticks={ticks} />
      </Profiler>,
    );
    act(() => {
      ticks.set(tickFixture({ heart_rate_bpm: 0 }));
      vi.runAllTimers();
    });
    const hr = screen.getByText('HR').closest('div')!;
    expect(hr).toHaveTextContent('—');
    act(() => {
      ticks.set(tickFixture({ heart_rate_bpm: 72 }));
      vi.runAllTimers();
    });
    expect(hr).toHaveTextContent('72');
    expect(renders).toBe(1);
  });

  it('reports the heart rate source status', () => {
    const { rerender } = render(
      <MetricGrid ticks={new TickStore()} hr={{ source: 'ble', target: 'AA:BB', connected: false, error: 'device not found' }} />,
    );
    expect(screen.getByText('Bluetooth: device not found')).toBeInTheDocument();
    rerender(<MetricGrid ticks={new TickStore()} hr={{ source: 'ha', target: 'sensor.hr', connected: true }} />);
    expect(screen.getByText('Home Assistant: connected')).toBeInTheDocument();
  });

  it('ERG mode adds a Target tile without re-rendering', () => {
    const ticks = new TickStore();
    let renders = 0;
    render(
      <Profiler id="g" onRender={() => renders++}>
        <MetricGrid ticks={ticks} erg />
      </Profiler>,
    );
    expect(screen.getByText('Target')).toBeInTheDocument();
    act(() => {
      ticks.set(tickFixture({ target_power_w: 250, power_w: 241 }));
      vi.runAllTimers();
    });
    expect(screen.getByText('250')).toBeInTheDocument();
    expect(renders).toBe(1);
  });

  describe('phone width', () => {
    beforeEach(() => {
      vi.stubGlobal('matchMedia', (query: string) => ({
        matches: query.includes('max-width: 599px'),
        media: query,
        addEventListener: () => {},
        removeEventListener: () => {},
      }));
    });
    afterEach(() => vi.unstubAllGlobals());

    it('shows four metrics until More metrics is pressed, without extra renders per tick', async () => {
      vi.useRealTimers();
      const ticks = new TickStore();
      let renders = 0;
      render(
        <Profiler id="g" onRender={() => renders++}>
          <MetricGrid ticks={ticks} />
        </Profiler>,
      );
      const labels = () => Array.from(document.querySelectorAll('.metric-label')).map((e) => e.textContent);
      expect(labels()).toEqual(['Power', 'Speed', 'Cadence', 'Level']);
      expect(renders).toBe(1);
      const more = screen.getByRole('button', { name: 'More metrics' });
      expect(more).toHaveAttribute('aria-expanded', 'false');
      await userEvent.click(more);
      expect(more).toHaveAttribute('aria-expanded', 'true');
      expect(labels()).toEqual(expect.arrayContaining(['Distance', 'Time', 'HR']));
      await userEvent.click(more);
      expect(labels()).toHaveLength(4);
    });

    it('route mode swaps Cadence for Grade in the four', () => {
      render(<MetricGrid ticks={new TickStore()} route />);
      const labels = Array.from(document.querySelectorAll('.metric-label')).map((e) => e.textContent);
      expect(labels).toEqual(['Power', 'Speed', 'Grade', 'Level']);
    });
  });
});
