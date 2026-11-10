import { useMemo } from 'react';
import type { EChartsCoreOption } from 'echarts/core';
import { ChartBox } from '@/features/charts/ChartBox';
import { alpha, axisText, gridLine, tooltipBase, useChartTheme, type ChartTheme } from '@/features/charts/theme';

const H = 200;

export interface Bar {
  label: string;
  value: number;
}

/** ECharts option: rounded bars, the latest period in full colour, value labels when there is room. */
export function totalsOption(bars: Bar[], fmt: (v: number) => string, t: ChartTheme): EChartsCoreOption {
  const labelled = bars.length <= 12;
  return {
    animationDuration: 500,
    grid: { left: labelled ? 12 : 52, right: 12, top: 24, bottom: 26 },
    tooltip: {
      ...tooltipBase(t),
      trigger: 'axis',
      confine: true,
      axisPointer: { type: 'shadow', shadowStyle: { color: alpha(t.chalk, 0.06) } },
      formatter: (items: { name: string; value: number }[]) =>
        `<span style="color:${t.dim}">${items[0].name}</span><br/><b style="font-size:14px">${fmt(items[0].value)}</b>`,
    },
    xAxis: {
      type: 'category',
      data: bars.map((b) => b.label),
      axisTick: { show: false },
      axisLine: { lineStyle: { color: alpha(t.chalk, 0.12) } },
      axisLabel: { ...axisText(t), hideOverlap: true },
    },
    yAxis: {
      type: 'value',
      min: 0,
      splitNumber: 3,
      axisLabel: { ...axisText(t), show: !labelled, formatter: (v: number) => fmt(v) },
      splitLine: gridLine(t),
    },
    series: [
      {
        type: 'bar',
        barMaxWidth: 28,
        barCategoryGap: '30%',
        data: bars.map((b, i) => ({
          value: b.value,
          itemStyle: { color: i === bars.length - 1 ? t.accent : alpha(t.accent, 0.5), borderRadius: [5, 5, 0, 0] },
        })),
        label: labelled ? { show: true, position: 'top', color: t.dim, fontFamily: t.font, fontSize: 11, formatter: (q: { value: number }) => (q.value > 0 ? fmt(q.value) : '') } : { show: false },
      },
    ],
  };
}

/** One bar per period. `fmt` formats a value for labels and the tooltip. */
export function TotalsChart({ title, bars, fmt }: { title: string; bars: Bar[]; fmt: (v: number) => string }) {
  const t = useChartTheme();
  const option = useMemo(() => totalsOption(bars, fmt, t), [bars, fmt, t]);
  const latest = bars[bars.length - 1];
  return (
    <section className="flex flex-col gap-2">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3">
        <h3 className="label">{title}</h3>
        {latest && (
          <p data-testid="totals-readout" className="numeral text-sm text-dim">
            {latest.label}: {fmt(latest.value)}
          </p>
        )}
      </div>
      <ChartBox option={option} height={H} label={`${title} per period`} />
    </section>
  );
}
