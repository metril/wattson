import { useMemo } from 'react';
import type { EChartsCoreOption } from 'echarts/core';
import { ChartBox } from '@/features/charts/ChartBox';
import { alpha, areaGradient, axisText, glow, gridLine, tooltipBase, useChartTheme, type ChartTheme } from '@/features/charts/theme';

const TICKS = [1, 5, 15, 60, 300, 1200, 3600, 7200];
const H = 220;

export interface CurveData {
  dur_s: number;
  watts: number;
  w_per_kg?: number;
}

/** 5 -> "5 s", 300 -> "5 min", 3600 -> "1 h". */
export function durLabel(s: number): string {
  if (s < 60) return `${s} s`;
  if (s < 3600) return `${Math.round(s / 60)} min`;
  return `${+(s / 3600).toFixed(1)} h`;
}

/** ECharts option: best power per duration on a log-time axis, with an optional dashed ride overlay. */
export function curveOption(points: CurveData[], ride: CurveData[] | undefined, rideLabel: string, t: ChartTheme): EChartsCoreOption {
  const xs = (pts: CurveData[]) => pts.map((q) => [Math.log10(q.dur_s), q.watts, q.dur_s, q.w_per_kg ?? 0]);
  const lo = points.length ? Math.log10(points[0].dur_s) : 0;
  const hi = points.length ? Math.log10(points[points.length - 1].dur_s) : 1;
  const ticks = TICKS.map(Math.log10).filter((v) => v >= lo - 1e-9 && v <= hi + 1e-9);
  const rideByDur = new Map((ride ?? []).map((q) => [q.dur_s, q.watts]));
  return {
    animationDuration: 600,
    grid: { left: 48, right: 16, top: 16, bottom: 28 },
    tooltip: {
      ...tooltipBase(t),
      trigger: 'axis',
      confine: true,
      formatter: (items: { seriesIndex: number; value: number[] }[]) => {
        const v = (items.find((i) => i.seriesIndex === 0) ?? items[0]).value;
        const mine = rideByDur.get(v[2]);
        return (
          `<span style="color:${t.dim}">${durLabel(v[2])}</span><br/><b style="color:${t.power};font-size:14px">${Math.round(v[1])} W</b>` +
          (v[3] ? ` <span style="color:${t.dim}">${v[3].toFixed(2)} W/kg</span>` : '') +
          (mine !== undefined ? `<br/><span style="color:${t.dim}">${rideLabel}</span> <b>${Math.round(mine)} W</b>` : '')
        );
      },
    },
    xAxis: {
      type: 'value',
      min: lo,
      max: hi,
      axisLine: { show: false },
      axisTick: { show: false },
      axisLabel: { ...axisText(t), customValues: ticks, formatter: (v: number) => durLabel(Math.round(10 ** v)), hideOverlap: true },
      splitLine: { ...gridLine(t), customValues: ticks },
    },
    yAxis: { type: 'value', min: 0, splitNumber: 3, axisLabel: { ...axisText(t), formatter: (v: number) => `${v}` }, splitLine: gridLine(t) },
    series: [
      {
        type: 'line',
        data: xs(points),
        encode: { x: 0, y: 1 },
        showSymbol: false,
        symbolSize: 7,
        lineStyle: { color: t.power, width: 2.5, ...glow(t.power) },
        itemStyle: { color: t.power },
        areaStyle: { color: areaGradient(t.power, 0.35) },
        emphasis: { disabled: false },
      },
      ...(ride && ride.length
        ? [
            {
              type: 'line',
              data: xs(ride),
              encode: { x: 0, y: 1 },
              showSymbol: false,
              lineStyle: { color: t.chalk, width: 2, type: 'dashed' as const, shadowColor: alpha(t.chalk, 0.4), shadowBlur: 6 },
              itemStyle: { color: t.chalk },
            },
          ]
        : []),
    ],
  };
}

/**
 * Best power per duration on a log-scale time axis. `points` is the main
 * (amber) curve; `ride` optionally overlays a single ride's curve.
 */
export function PowerCurve(p: { points: CurveData[]; ride?: CurveData[]; rideLabel?: string; label?: string }) {
  const t = useChartTheme();
  const label = p.label ?? 'Best power';
  const rideLabel = p.rideLabel ?? 'this ride';
  const option = useMemo(() => curveOption(p.points, p.ride, rideLabel, t), [p.points, p.ride, rideLabel, t]);
  const first = p.points[0]?.dur_s ?? 0;
  const last = p.points[p.points.length - 1]?.dur_s ?? 0;
  const peak = p.points.reduce((m, q) => Math.max(m, q.watts), 0);
  return (
    <section className="flex flex-col gap-2">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3">
        <h3 className="label">{label}</h3>
        {peak > 0 && <p className="numeral text-sm text-dim">peak {Math.round(peak)} W</p>}
      </div>
      <ChartBox option={option} height={H} label={`${label} from ${durLabel(first)} to ${durLabel(last)}`} />
      {p.ride && <p className="text-xs text-dim">Solid: your best. Dashed: {rideLabel}.</p>}
    </section>
  );
}
