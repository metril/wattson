import { useEffect, useRef } from 'react';
import { animate } from 'motion';

/** A number that counts up from 0 on mount. Renders the final text at once under reduced motion and in tests. */
export function CountUp({ value, digits = 0 }: { value: number; digits?: number }) {
  const ref = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el || import.meta.env.MODE === 'test' || window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return;
    const c = animate(0, value, { duration: 0.7, ease: 'easeOut', onUpdate: (v) => (el.textContent = v.toFixed(digits)) });
    return () => c.stop();
  }, [value, digits]);
  return <span ref={ref}>{value.toFixed(digits)}</span>;
}
