import { useMemo } from 'react';
import type { EChartsCoreOption } from 'echarts/core';
import { ChartBox } from '@/features/charts/ChartBox';
import { alpha, areaGradient, axisText, glow, gridLine, tooltipBase, useChartTheme, type ChartTheme } from '@/features/charts/theme';

const H = 220;

export interface FitnessPoint {
  date: string;
  ctl: number;
  atl: number;
  tsb: number;
}

const SERIES = [
  { key: 'ctl', short: 'CTL', name: 'Fitness (CTL)', color: 'accent' },
  { key: 'atl', short: 'ATL', name: 'Fatigue (ATL)', color: 'hr' },
  { key: 'tsb', short: 'TSB', name: 'Form (TSB)', color: 'ok' },
] as const;

/** ECharts option: CTL, ATL and TSB lines over days, a zero line, and a form-shaded area. */
export function fitnessOption(days: FitnessPoint[], t: ChartTheme): EChartsCoreOption {
  return {
    animationDuration: 600,
    grid: { left: 40, right: 14, top: 14, bottom: 26 },
    tooltip: {
      ...tooltipBase(t),
      trigger: 'axis',
      confine: true,
      formatter: (items: { axisValue: string; seriesName: string; value: number; color: string }[]) =>
        `<span style="color:${t.dim}">${items[0].axisValue}</span>` +
        items.map((i) => `<br/><span style="color:${i.color}">●</span> ${i.seriesName} <b>${i.value.toFixed(0)}</b>`).join(''),
    },
    xAxis: {
      type: 'category',
      data: days.map((d) => d.date),
      boundaryGap: false,
      axisTick: { show: false },
      axisLine: { show: false },
      axisLabel: { ...axisText(t), hideOverlap: true, formatter: (v: string) => v.slice(5) },
    },
    yAxis: { type: 'value', scale: true, splitNumber: 4, axisLabel: { ...axisText(t), formatter: (v: number) => v.toFixed(0) }, splitLine: gridLine(t) },
    series: SERIES.map((s) => {
      const c = t[s.color];
      return {
        type: 'line',
        name: s.name,
        data: days.map((d) => d[s.key]),
        showSymbol: false,
        smooth: 0.2,
        lineStyle: { color: c, width: 2, ...glow(c) },
        itemStyle: { color: c },
        areaStyle: s.key === 'ctl' ? { color: areaGradient(c, 0.25) } : undefined,
        emphasis: { disabled: true },
        markLine:
          s.key === 'tsb'
            ? { silent: true, symbol: 'none', label: { show: false }, lineStyle: { color: alpha(t.chalk, 0.35), type: 'dashed' }, data: [{ yAxis: 0 }] }
            : undefined,
      };
    }),
  };
}

/** Daily CTL, ATL and TSB lines. The tooltip reads out one day; the header shows the latest. */
export function FitnessChart({ days }: { days: FitnessPoint[] }) {
  const t = useChartTheme();
  const option = useMemo(() => fitnessOption(days, t), [days, t]);
  const shown = days[days.length - 1];
  return (
    <section className="flex flex-col gap-2">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3">
        <h3 className="label">Fitness, fatigue and form</h3>
        {shown && (
          <p data-testid="fitness-readout" className="numeral text-sm text-dim">
            {shown.date} · {SERIES.map((s) => `${s.short} ${shown[s.key].toFixed(0)}`).join(' · ')}
          </p>
        )}
      </div>
      <ChartBox option={option} height={H} label="Fitness, fatigue and form over time" />
      <ul className="flex flex-wrap gap-x-4 text-xs text-dim">
        {SERIES.map((s) => (
          <li key={s.key} className="flex items-center gap-1.5">
            <span aria-hidden className="inline-block h-0.5 w-4 rounded" style={{ background: t[s.color] }} />
            {s.name}
          </li>
        ))}
      </ul>
    </section>
  );
}
