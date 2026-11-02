import { useState } from 'react';
import { Button } from './Button';
import { Field } from './Field';
import type { ReactNode } from 'react';

/**
 * Write-only secret field. It is never prefilled: untouched (or emptied) reports
 * `undefined` (keep the stored value), typed text reports the text, and the
 * explicit Clear button reports "" (delete the stored value). Re-key it after a save.
 */
export function SecretInput({
  label,
  isSet,
  onChange,
  hint,
  error,
  clearable = true,
}: {
  label: string;
  isSet: boolean;
  onChange: (v: string | undefined) => void;
  hint?: ReactNode;
  error?: string;
  /** False for secrets the server requires (e.g. an OIDC client secret). */
  clearable?: boolean;
}) {
  const [value, setValue] = useState('');
  const [cleared, setCleared] = useState(false);

  return (
    <Field
      label={label}
      hint={hint}
      error={error}
      type="password"
      autoComplete="new-password"
      value={cleared ? '' : value}
      disabled={cleared}
      placeholder={cleared ? 'Will be cleared on save' : isSet ? '•••••• (saved)' : ''}
      onChange={(e) => {
        setValue(e.target.value);
        onChange(e.target.value === '' ? undefined : e.target.value);
      }}
      trailing={
        isSet && clearable ? (
          cleared ? (
            <Button
              aria-label={`Undo clear ${label.toLowerCase()}`}
              onClick={() => {
                setCleared(false);
                onChange(value === '' ? undefined : value);
              }}
            >
              Undo
            </Button>
          ) : (
            <Button aria-label={`Clear ${label.toLowerCase()}`} onClick={() => {
              setCleared(true);
              onChange('');
            }}>
              Clear
            </Button>
          )
        ) : undefined
      }
    />
  );
}
