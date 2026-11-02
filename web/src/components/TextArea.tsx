import { useId, type ReactNode, type TextareaHTMLAttributes } from 'react';

export function TextArea({
  label,
  hint,
  error,
  className = '',
  ...rest
}: Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, 'id'> & { label: string; hint?: ReactNode; error?: string }) {
  const id = useId();
  const describedBy = [hint && `${id}-hint`, error && `${id}-err`].filter(Boolean).join(' ');
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-sm font-medium text-dim">
        {label}
      </label>
      <textarea
        id={id}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy || undefined}
        className={`min-h-24 w-full rounded-control border border-line bg-raised px-3 py-2 text-chalk placeholder:text-faint aria-[invalid=true]:border-danger ${className}`}
        {...rest}
      />
      {hint && (
        <p id={`${id}-hint`} className="text-xs text-dim">
          {hint}
        </p>
      )}
      {error && (
        <p id={`${id}-err`} className="text-xs text-danger">
          {error}
        </p>
      )}
    </div>
  );
}
