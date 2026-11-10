import { useEffect, useState } from 'react';

/** Colours and type for every chart, read from the CSS tokens so dark, light and cockpit all work. */
export interface ChartTheme {
  chalk: string;
  dim: string;
  label: string;
  line: string;
  overlay: string;
  accent: string;
  ok: string;
  danger: string;
  power: string;
  hr: string;
  cadence: string;
  speed: string;
  elev: string;
  zones: string[];
  font: string;
  /** A token name (--m-power) is resolved to its colour; anything else is returned as is. */
  resolve(c: string): string;
}

const FALLBACK: Record<string, string> = {
  '--chalk': '#eef0f2',
  '--dim': '#a3b6bb',
  '--label': '#8d9aa6',
  '--line': '#3a424d',
  '--overlay': '#252a33',
  '--accent': '#54a9ff',
  '--ok': '#6fd36a',
  '--danger': '#ff6b63',
  '--m-power': '#ffa63d',
  '--m-hr': '#ff5a6e',
  '--m-cadence': '#4fd67a',
  '--m-speed': '#4aa3ff',
  '--m-elev': '#8fa1b3',
  '--zone-1': '#7f9bb3',
  '--zone-2': '#3fbfae',
  '--zone-3': '#7fd05a',
  '--zone-4': '#ecc93c',
  '--zone-5': '#f5923d',
  '--zone-6': '#f45b50',
  '--zone-7': '#cc66e6',
};

/** Reads the theme from the document's computed custom properties. */
export function readTheme(el: Element = document.documentElement): ChartTheme {
  const cs = getComputedStyle(el);
  const v = (name: string) => cs.getPropertyValue(name).trim() || FALLBACK[name] || '#888888';
  return {
    chalk: v('--chalk'),
    dim: v('--dim'),
    label: v('--label'),
    line: v('--line'),
    overlay: v('--overlay'),
    accent: v('--accent'),
    ok: v('--ok'),
    danger: v('--danger'),
    power: v('--m-power'),
    hr: v('--m-hr'),
    cadence: v('--m-cadence'),
    speed: v('--m-speed'),
    elev: v('--m-elev'),
    zones: [1, 2, 3, 4, 5, 6, 7].map((i) => v(`--zone-${i}`)),
    font: cs.getPropertyValue('--font-display').trim() || 'system-ui, sans-serif',
    resolve: (c) => (c.startsWith('--') ? v(c) : c),
  };
}

/** The theme, re-read when the OS scheme or the document's theme class changes. */
export function useChartTheme(): ChartTheme {
  const [theme, setTheme] = useState(() => readTheme());
  useEffect(() => {
    const refresh = () => setTheme(readTheme());
    const mq = window.matchMedia?.('(prefers-color-scheme: light)');
    mq?.addEventListener('change', refresh);
    const mo = new MutationObserver(refresh);
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'data-theme'] });
    return () => {
      mq?.removeEventListener('change', refresh);
      mo.disconnect();
    };
  }, []);
  return theme;
}

/** "#rrggbb" or "#rgb" with an alpha; other colour syntaxes are returned unchanged. */
export function alpha(color: string, a: number): string {
  const m = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(color.trim());
  if (!m) return color;
  let h = m[1];
  if (h.length === 3) h = [...h].map((c) => c + c).join('');
  const n = parseInt(h, 16);
  return `rgba(${(n >> 16) & 255},${(n >> 8) & 255},${n & 255},${a})`;
}

/** Vertical fill that fades from the colour to nothing. */
export function areaGradient(color: string, top = 0.4) {
  return {
    type: 'linear' as const,
    x: 0,
    y: 0,
    x2: 0,
    y2: 1,
    colorStops: [
      { offset: 0, color: alpha(color, top) },
      { offset: 1, color: alpha(color, 0) },
    ],
  };
}

export function axisText(t: ChartTheme) {
  return { color: t.dim, fontFamily: t.font, fontSize: 11 };
}

export function gridLine(t: ChartTheme) {
  return { show: true, lineStyle: { color: alpha(t.chalk, 0.07), type: 'solid' as const } };
}

export function tooltipBase(t: ChartTheme) {
  return {
    backgroundColor: t.overlay,
    borderColor: alpha(t.chalk, 0.12),
    borderWidth: 1,
    padding: [6, 10] as [number, number],
    textStyle: { color: t.chalk, fontFamily: t.font, fontSize: 12 },
    extraCssText: 'box-shadow:0 8px 24px -8px rgba(0,0,0,.6);border-radius:8px;',
    axisPointer: { type: 'line' as const, lineStyle: { color: alpha(t.chalk, 0.5), width: 1 } },
  };
}

/** Glow behind a line: a blurred shadow of the same colour. */
export function glow(color: string) {
  return { shadowColor: alpha(color, 0.55), shadowBlur: 8 };
}
