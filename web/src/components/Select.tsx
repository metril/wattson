import { useId, type ReactNode, type SelectHTMLAttributes } from 'react';

export function Select({
  label,
  hint,
  children,
  ...rest
}: Omit<SelectHTMLAttributes<HTMLSelectElement>, 'id'> & { label: string; hint?: ReactNode }) {
  const id = useId();
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-sm font-medium text-dim">
        {label}
      </label>
      <select
        id={id}
        data-nav
        aria-describedby={hint ? `${id}-hint` : undefined}
        className="min-h-touch w-full rounded-control border border-line bg-raised px-3 text-chalk"
        {...rest}
      >
        {children}
      </select>
      {hint && (
        <p id={`${id}-hint`} className="text-xs text-dim">
          {hint}
        </p>
      )}
    </div>
  );
}
