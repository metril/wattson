import type { EChartsCoreOption } from 'echarts/core';
import { useEffect, useRef } from 'react';
import type { ECharts } from 'echarts/core';
import ReactEChartsCore from 'echarts-for-react/lib/core';
import { echarts } from './echarts';

/** The ECharts canvas. Loaded lazily by ChartBox so no page pays for it until it draws a chart. */
export default function EChart({ option, height, group, resetToken, onZoom }: { option: EChartsCoreOption; height: number; group?: string; resetToken?: number; onZoom?: (zoomed: boolean) => void }) {
  const chartRef = useRef<ECharts | null>(null);
  useEffect(() => {
    if (resetToken) chartRef.current?.dispatchAction({ type: 'dataZoom', start: 0, end: 100 });
  }, [resetToken]);
  return (
    <ReactEChartsCore
      echarts={echarts}
      option={option}
      notMerge
      lazyUpdate
      style={{ height, width: '100%' }}
      opts={{ renderer: 'canvas' }}
      onChartReady={(chart) => {
        chartRef.current = chart;
        if (onZoom) {
          // Drag-to-select is always armed; the zoom range then follows every connected chart.
          chart.dispatchAction({ type: 'takeGlobalCursor', key: 'dataZoomSelect', dataZoomSelectActive: true });
          chart.on('datazoom', (e: unknown) => {
            const ev = e as { start?: number; end?: number; batch?: { start?: number; end?: number }[] };
            const z = ev.batch?.[0] ?? ev;
            onZoom((z.start ?? 0) > 0.01 || (z.end ?? 100) < 99.99);
          });
        }
        if (!group) return;
        chart.group = group;
        echarts.connect(group); // charts of one group share the crosshair and tooltip
      }}
    />
  );
}
