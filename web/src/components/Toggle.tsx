import { useId, type ReactNode } from 'react';

/**
 * Switch whose whole label row is the hit area: at least 48 x 48 px even when
 * `srOnly` hides the text (table cells), while the 24 px control stays centred.
 */
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
    <div>
      <label
        className={`flex min-h-touch min-w-touch items-center gap-3 ${srOnly ? 'justify-center' : ''} ${disabled ? 'cursor-not-allowed' : 'cursor-pointer'}`}
      >
        <input
          type="checkbox"
          data-nav
          role="switch"
          checked={checked}
          disabled={disabled}
          aria-describedby={hint ? `${id}-hint` : undefined}
          onChange={(e) => onChange(e.target.checked)}
          className="size-6 shrink-0 accent-accent disabled:opacity-50"
        />
        <span className={srOnly ? 'sr-only' : 'min-w-0'}>{label}</span>
      </label>
      {hint && (
        <p id={`${id}-hint`} className="pb-2 pl-9 text-xs text-dim">
          {hint}
        </p>
      )}
    </div>
  );
}
