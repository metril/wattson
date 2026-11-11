import { lazy, Suspense } from 'react';
import type { EChartsCoreOption } from 'echarts/core';

const EChart = lazy(() => import('./EChart'));

/** A sunken well with an accessible name around a lazily loaded ECharts canvas. */
export function ChartBox({ option, height, label, group, resetToken, onZoom }: { option: EChartsCoreOption; height: number; label: string; group?: string; resetToken?: number; onZoom?: (zoomed: boolean) => void }) {
  return (
    <div role="img" aria-label={label} className="animate-[chart-in_0.5s_ease-out] overflow-hidden rounded-control bg-sunken" style={{ height }}>
      <Suspense fallback={null}>
        <EChart option={option} height={height} group={group} resetToken={resetToken} onZoom={onZoom} />
      </Suspense>
    </div>
  );
}
