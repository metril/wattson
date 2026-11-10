import { render, screen } from '@testing-library/react';
import { readTheme } from '@/features/charts/theme';
import { SeriesChart, seriesOption } from './SeriesChart';

const t = readTheme();
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Opt = any;

describe('SeriesChart', () => {
  it('names the chart with its label and range, and shows the range in the header', () => {
    render(<SeriesChart x={[0, 1, 2]} y={[100, 200, 150]} label="Power" unit="W" color="--m-power" />);
    expect(screen.getByRole('img', { name: 'Power over distance, 100 to 200 W' })).toBeInTheDocument();
    expect(screen.getByText('100–200 W')).toBeInTheDocument();
  });

  it('builds a gradient line over [distance, value] pairs and formats the tooltip with the x unit', () => {
    const o = seriesOption({ x: [0, 1, 2], y: [10, 20, 15], unit: 'mph', xUnit: 'mi', color: '--m-speed' }, t) as Opt;
    expect(o.series[0].data).toEqual([[0, 10], [1, 20], [2, 15]]);
    expect(o.series[0].areaStyle.color.colorStops).toHaveLength(2);
    expect(o.xAxis.name).toBe('mi');
    expect(o.tooltip.formatter([{ value: [1, 20] }])).toContain('20.0 mph');
    expect(o.tooltip.formatter([{ value: [1, 20] }])).toContain('1.00 mi');
  });

  it('draws power zone bands behind the line, clipped to the axis', () => {
    const bands = [
      { from: 0, to: 110, color: '--zone-1' },
      { from: 110, to: 150, color: '--zone-2' },
      { from: 5000, to: null, color: '--zone-7' },
    ];
    const o = seriesOption({ x: [0, 1], y: [100, 200], unit: 'W', color: '--m-power', bands }, t) as Opt;
    const area = o.series[0].markArea.data;
    expect(area).toHaveLength(2); // the band above the data is dropped
    expect(area[0][0].yAxis).toBe(0);
    expect(area[1][1].yAxis).toBe(150);
  });

  it('lets elevation float and zero-bases other series', () => {
    expect((seriesOption({ x: [0, 1], y: [100, 120], unit: 'm', color: '#fff', zero: false }, t) as Opt).yAxis.min).toBe('dataMin');
    expect((seriesOption({ x: [0, 1], y: [100, 120], unit: 'W', color: '#fff' }, t) as Opt).yAxis.min).toBe(0);
  });
});
