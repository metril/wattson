import type { ReactNode } from 'react';

/** Standard page header: title, optional subtitle, right-aligned actions; sticks to the top with a blurred backdrop. */
export function PageHeader({ title, subtitle, actions, className = '' }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode; className?: string }) {
  return (
    <header
      className={`md:sticky top-0 z-10 -mx-4 -mt-4 mb-1 flex flex-wrap items-end gap-x-4 gap-y-2 border-b border-line-soft bg-ground/80 px-4 py-3 backdrop-blur-md md:-mx-8 md:-mt-8 md:px-8 md:pt-6 ${className}`}
    >
      <div className="min-w-0 flex-1 basis-48">
        <h1 className="numeral truncate text-2xl leading-tight">{title}</h1>
        {subtitle && <p className="text-sm text-dim">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </header>
  );
}
