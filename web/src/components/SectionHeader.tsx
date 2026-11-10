import type { ReactNode } from 'react';

/** Title row of a card or page section, with optional hint text and right-aligned actions. */
export function SectionHeader({ title, hint, actions, as: H = 'h2' }: { title: string; hint?: ReactNode; actions?: ReactNode; as?: 'h2' | 'h3' }) {
  return (
    <div className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-1">
      <div className="min-w-0 flex-1">
        <H className="label">{title}</H>
        {hint && <p className="text-xs text-dim">{hint}</p>}
      </div>
      {actions}
    </div>
  );
}
