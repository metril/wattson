import type { HTMLAttributes } from 'react';

type Variant = 'flat' | 'raised' | 'sunken';

const variants: Record<Variant, string> = {
  flat: 'border border-line-soft bg-panel',
  raised: 'border border-line-soft bg-panel shadow-panel transition-[transform,box-shadow] duration-200 motion-safe:hover:-translate-y-px',
  sunken: 'bg-sunken',
};

/** A surface. `raised` (default) has the soft shadow, `flat` only the hairline, `sunken` is a chart well. */
export function Card({ className = '', variant = 'raised', ...rest }: HTMLAttributes<HTMLDivElement> & { variant?: Variant }) {
  return <div className={`rounded-panel p-5 ${variants[variant]} ${className}`} {...rest} />;
}
