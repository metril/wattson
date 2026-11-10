import type { EChartsCoreOption } from 'echarts/core';
import ReactEChartsCore from 'echarts-for-react/lib/core';
import { echarts } from './echarts';

/** The ECharts canvas. Loaded lazily by ChartBox so no page pays for it until it draws a chart. */
export default function EChart({ option, height, group }: { option: EChartsCoreOption; height: number; group?: string }) {
  return (
    <ReactEChartsCore
      echarts={echarts}
      option={option}
      notMerge
      lazyUpdate
      style={{ height, width: '100%' }}
      opts={{ renderer: 'canvas' }}
      onChartReady={(chart) => {
        if (!group) return;
        chart.group = group;
        echarts.connect(group); // charts of one group share the crosshair and tooltip
      }}
    />
  );
}
