import { useMemo } from 'react';
import type { EChartsCoreOption } from 'echarts/core';
import { ChartBox } from '@/features/charts/ChartBox';
import { alpha, areaGradient, axisText, glow, gridLine, tooltipBase, useChartTheme, type ChartTheme } from '@/features/charts/theme';
import { useMedia } from '@/lib/use-media';

export interface Band {
  from: number;
  /** Upper edge; null runs to the top of the chart. */
  to: number | null;
  color: string;
}

export interface SeriesOptions {
  x: number[];
  y: number[];
  unit: string;
  /** Unit of x (distance); km by default. */
  xUnit?: string;
  /** A hex colour or a CSS token name such as --m-power. */
  color: string;
  /** Start the y axis at zero (default); elevation floats instead. */
  zero?: boolean;
  /** Coloured horizontal bands behind the line (power zones). */
  bands?: Band[];
}

const decimalsOf = (unit: string) => (unit === 'km/h' || unit === 'mph' ? 1 : 0);

/** ECharts option for one series over distance: gradient area, glowing line, zone bands, crosshair tooltip. */
export function seriesOption(p: SeriesOptions, t: ChartTheme): EChartsCoreOption {
  const xUnit = p.xUnit ?? 'km';
  const n = Math.min(p.x.length, p.y.length);
  const color = t.resolve(p.color);
  const d = decimalsOf(p.unit);
  let max = -Infinity;
  for (let i = 0; i < n; i++) max = Math.max(max, p.y[i]);
  if (!Number.isFinite(max) || max <= 0) max = 1;
  const top = Math.ceil(max * 1.08);
  const data = Array.from({ length: n }, (_, i) => [p.x[i], p.y[i]]);
  const bands = (p.bands ?? [])
    .filter((b) => b.from < top)
    .map((b) => [
      { yAxis: b.from, itemStyle: { color: alpha(t.resolve(b.color), 0.12) } },
      { yAxis: Math.min(b.to ?? top, top) },
    ]);
  return {
    animationDuration: 600,
    animationEasing: 'cubicOut',
    grid: { left: 44, right: 14, top: 12, bottom: 26 },
    tooltip: {
      ...tooltipBase(t),
      trigger: 'axis',
      confine: true,
      formatter: (items: { value: [number, number] }[]) => {
        const [x, y] = items[0].value;
        return `<span style="color:${t.dim}">${x.toFixed(2)} ${xUnit}</span><br/><b style="color:${color};font-size:14px">${y.toFixed(d)} ${p.unit}</b>`;
      },
    },
    xAxis: {
      type: 'value',
      min: n ? p.x[0] : 0,
      max: n ? p.x[n - 1] : 1,
      axisLine: { show: false },
      axisTick: { show: false },
      splitLine: { show: false },
      axisLabel: { ...axisText(t), formatter: (v: number) => (v % 1 === 0 ? `${v}` : v.toFixed(1)), hideOverlap: true },
      name: xUnit,
      nameLocation: 'end',
      nameTextStyle: { ...axisText(t), align: 'right', verticalAlign: 'top', padding: [14, 0, 0, 0] },
    },
    yAxis: {
      type: 'value',
      min: p.zero === false ? 'dataMin' : 0,
      max: top,
      splitNumber: 3,
      axisLabel: { ...axisText(t), formatter: (v: number) => v.toFixed(0) },
      splitLine: gridLine(t),
    },
    series: [
      {
        type: 'line',
        data,
        showSymbol: false,
        sampling: 'lttb',
        lineStyle: { color, width: 2, ...glow(color) },
        itemStyle: { color },
        areaStyle: { color: areaGradient(color) },
        emphasis: { disabled: true },
        markArea: bands.length ? { silent: true, data: bands } : undefined,
      },
    ],
  };
}

/** One ride series over distance. Charts sharing a `group` share their crosshair and tooltip. */
export function SeriesChart(p: SeriesOptions & { label: string; height?: number; group?: string }) {
  const t = useChartTheme();
  const roomy = useMedia('(min-width: 768px)');
  const h = p.height ?? (roomy ? 200 : 160);
  const bandKey = JSON.stringify(p.bands ?? []);
  const option = useMemo(() => seriesOption(p, t), [p.x, p.y, p.unit, p.xUnit, p.color, p.zero, bandKey, t]); // eslint-disable-line react-hooks/exhaustive-deps
  const d = decimalsOf(p.unit);
  let lo = Infinity;
  let hi = -Infinity;
  for (const v of p.y) {
    if (v < lo) lo = v;
    if (v > hi) hi = v;
  }
  if (!Number.isFinite(lo)) lo = hi = 0;
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between gap-3">
        <h3 className="label flex items-center gap-2">
          <span aria-hidden className="size-2 rounded-full" style={{ background: t.resolve(p.color) }} />
          {p.label}
        </h3>
        <p className="numeral text-sm text-dim">
          {lo.toFixed(d)}–{hi.toFixed(d)} {p.unit}
        </p>
      </div>
      <ChartBox
        option={option}
        height={h}
        group={p.group}
        label={`${p.label} over distance, ${lo.toFixed(d)} to ${hi.toFixed(d)} ${p.unit}`}
      />
    </section>
  );
}
