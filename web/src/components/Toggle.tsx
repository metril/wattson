import { useId, type ReactNode } from 'react';

/** Switch with a 48 px hit area. `srOnly` keeps the label for assistive tech only (table cells). */
export function Toggle({
  label,
  hint,
  checked,
  onChange,
  disabled,
  srOnly,
}: {
  label: string;
  hint?: ReactNode;
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
  srOnly?: boolean;
}) {
  const id = useId();
  return (
    <div className="flex items-start gap-3">
      <input
        id={id}
        type="checkbox"
        role="switch"
        checked={checked}
        disabled={disabled}
        aria-describedby={hint ? `${id}-hint` : undefined}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-3 size-6 shrink-0 accent-accent disabled:opacity-50"
      />
      <div className={srOnly ? 'sr-only' : 'min-w-0 py-2'}>
        <label htmlFor={id} className="block min-h-6 leading-6">
          {label}
        </label>
        {hint && (
          <p id={`${id}-hint`} className="text-xs text-dim">
            {hint}
          </p>
        )}
      </div>
    </div>
  );
}
