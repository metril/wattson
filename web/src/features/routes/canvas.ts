import { useEffect, useRef, useState, type RefObject } from 'react';

/** CSS colour token (--ok, --accent, ...) read at draw time so light/dark both work. */
export const tok = (name: string, fallback: string) =>
  getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;

/** Width of an element, following resizes. */
export function useWidth(ref: RefObject<HTMLElement>): number {
  const [w, setW] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    setW(el.clientWidth);
    const ro = new ResizeObserver(() => setW(el.clientWidth));
    ro.observe(el);
    return () => ro.disconnect();
  }, [ref]);
  return w;
}

/** Size a canvas for the device (pixel ratio capped at 1.5) and return a context in CSS pixels, or null. */
export function prepCanvas(c: HTMLCanvasElement, w: number, h: number): CanvasRenderingContext2D | null {
  const dpr = Math.min(window.devicePixelRatio || 1, 1.5);
  c.width = Math.round(w * dpr);
  c.height = Math.round(h * dpr);
  const ctx = c.getContext('2d');
  if (!ctx) return null;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  return ctx;
}

export function useCanvasBox<T extends HTMLElement = HTMLDivElement>() {
  const box = useRef<T>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const width = useWidth(box as RefObject<HTMLElement>);
  return { box, canvas, width };
}
