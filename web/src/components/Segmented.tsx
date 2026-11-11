import * as ToggleGroup from '@radix-ui/react-toggle-group';

/** Segmented single-choice control (Radix ToggleGroup). Selecting the active segment keeps it selected. */
export function Segmented<T extends string>({
  label,
  value,
  onChange,
  options,
  className = '',
}: {
  label: string;
  value: T;
  onChange: (v: T) => void;
  options: { value: T; label: string }[];
  className?: string;
}) {
  return (
    <ToggleGroup.Root
      type="single"
      rovingFocus={false}
      value={value}
      aria-label={label}
      onValueChange={(v) => v && onChange(v as T)}
      className={`inline-flex gap-0.5 rounded-control bg-sunken p-0.5 ${className}`}
    >
      {options.map((o) => (
        <ToggleGroup.Item
          key={o.value}
          value={o.value}
          data-nav
          className="min-h-10 rounded-[0.375rem] px-3 text-sm font-medium text-dim transition-colors hover:text-chalk data-[state=on]:bg-accent-soft data-[state=on]:text-accent"
        >
          {o.label}
        </ToggleGroup.Item>
      ))}
    </ToggleGroup.Root>
  );
}
