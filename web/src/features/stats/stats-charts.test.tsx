import { render, screen } from '@testing-library/react';
import { readTheme } from '@/features/charts/theme';
import { fitnessOption, FitnessChart } from './FitnessChart';
import { curveOption, durLabel, PowerCurve } from './PowerCurve';
import { totalsOption, TotalsChart } from './TotalsChart';

const t = readTheme();
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Opt = any;

describe('durLabel', () => {
  it('formats seconds, minutes and hours', () => {
    expect([1, 45, 60, 300, 1200, 3600, 5400, 7200].map(durLabel)).toEqual([
      '1 s', '45 s', '1 min', '5 min', '20 min', '1 h', '1.5 h', '2 h',
    ]);
  });
});

describe('PowerCurve', () => {
  const pts = [
    { dur_s: 5, watts: 900, w_per_kg: 12 },
    { dur_s: 60, watts: 500, w_per_kg: 6.67 },
    { dur_s: 3600, watts: 250 },
  ];

  it('is named by its range and shows the peak', () => {
    render(<PowerCurve points={pts} />);
    expect(screen.getByRole('img', { name: /best power from 5 s to 1 h/i })).toBeInTheDocument();
    expect(screen.getByText('peak 900 W')).toBeInTheDocument();
  });

  it('puts duration ticks on a log axis and reads out watts, W/kg and the overlaid ride', () => {
    const o = curveOption(pts, [{ dur_s: 5, watts: 700 }], 'this ride', t) as Opt;
    expect(o.xAxis.axisLabel.customValues).toEqual([5, 15, 60, 300, 1200, 3600].map(Math.log10));
    expect(o.xAxis.axisLabel.formatter(Math.log10(300))).toBe('5 min');
    const html = o.tooltip.formatter([{ seriesIndex: 0, value: [Math.log10(5), 900, 5, 12] }]);
    expect(html).toContain('900 W');
    expect(html).toContain('12.00 W/kg');
    expect(html).toContain('this ride</span> <b>700 W');
    expect(o.series).toHaveLength(2);
    expect(o.series[1].lineStyle.type).toBe('dashed');
  });

  it('explains the dashed overlay', () => {
    render(<PowerCurve points={pts} ride={[{ dur_s: 5, watts: 700 }]} rideLabel="this ride" />);
    expect(screen.getByText(/Dashed: this ride/)).toBeInTheDocument();
  });
});

describe('FitnessChart', () => {
  const days = [
    { date: '2026-05-01', ctl: 10, atl: 20, tsb: -10 },
    { date: '2026-05-02', ctl: 12, atl: 15, tsb: -3 },
    { date: '2026-05-03', ctl: 14, atl: 10, tsb: 4 },
  ];

  it('shows the latest day in the header', () => {
    render(<FitnessChart days={days} />);
    expect(screen.getByTestId('fitness-readout')).toHaveTextContent('2026-05-03 · CTL 14 · ATL 10 · TSB 4');
  });

  it('draws three lines and a zero line on the form series', () => {
    const o = fitnessOption(days, t) as Opt;
    expect(o.series.map((s: Opt) => s.name)).toEqual(['Fitness (CTL)', 'Fatigue (ATL)', 'Form (TSB)']);
    expect(o.series[2].data).toEqual([-10, -3, 4]);
    expect(o.series[2].markLine.data).toEqual([{ yAxis: 0 }]);
  });
});

describe('TotalsChart', () => {
  const bars = [
    { label: 'W1', value: 10 },
    { label: 'W2', value: 30 },
  ];

  it('shows the latest period in the header', () => {
    render(<TotalsChart title="Distance per week" bars={bars} fmt={(v) => `${v} km`} />);
    expect(screen.getByTestId('totals-readout')).toHaveTextContent('W2: 30 km');
  });

  it('highlights the current period and labels bars when there are few', () => {
    const o = totalsOption(bars, (v) => `${v} km`, t) as Opt;
    expect(o.series[0].data[1].itemStyle.color).toBe(t.accent);
    expect(o.series[0].data[0].itemStyle.color).not.toBe(t.accent);
    expect(o.series[0].label.show).toBe(true);
    expect(o.tooltip.formatter([{ name: 'W2', value: 30 }])).toContain('30 km');
    const many = totalsOption(Array.from({ length: 20 }, (_, i) => ({ label: `${i}`, value: i })), String, t) as Opt;
    expect(many.series[0].label.show).toBe(false);
  });
});
