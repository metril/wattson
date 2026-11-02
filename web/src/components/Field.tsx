import { useId, type InputHTMLAttributes, type ReactNode } from 'react';

type Props = Omit<InputHTMLAttributes<HTMLInputElement>, 'id'> & {
  label: string;
  hint?: ReactNode;
  error?: string;
  /** Extra control rendered beside the input, e.g. a "Clear" button. */
  trailing?: ReactNode;
};

/** Labelled text input. Secret inputs must never be given a `value` prefill. */
export function Field({ label, hint, error, trailing, className = '', ...input }: Props) {
  const id = useId();
  const describedBy = [hint && `${id}-hint`, error && `${id}-err`].filter(Boolean).join(' ');
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-sm font-medium text-dim">
        {label}
      </label>
      <div className="flex gap-2">
        <input
          id={id}
          data-nav
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy || undefined}
          className={`min-h-touch w-full min-w-0 rounded-control border border-line bg-raised px-3 text-chalk placeholder:text-faint aria-[invalid=true]:border-danger ${className}`}
          {...input}
        />
        {trailing}
      </div>
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
