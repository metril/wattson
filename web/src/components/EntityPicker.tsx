import { useId, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { Field } from './Field';

const MAX_SHOWN = 50;

/**
 * Searchable Home Assistant entity field. The text is the entity id and can be
 * typed freely, so it still works when the list cannot be loaded.
 * Entities of `deviceClass` are listed first.
 */
export function EntityPicker({
  domain,
  label,
  value,
  onChange,
  deviceClass,
  hint,
}: {
  domain: 'number' | 'sensor';
  label: string;
  value: string;
  onChange: (id: string) => void;
  deviceClass?: string;
  hint?: string;
}) {
  const listId = useId();
  const [text, setText] = useState(value);
  const [open, setOpen] = useState(false);
  const q = useQuery({
    queryKey: ['ha-entities', domain],
    queryFn: async () => unwrap(await api.GET('/api/ha/entities', { params: { query: { domain } } })),
    retry: false,
    staleTime: 30_000,
  });

  const needle = text.trim().toLowerCase();
  const all = q.data?.entities ?? [];
  const match = (e: (typeof all)[number]) =>
    needle === '' || e.entity_id.toLowerCase().includes(needle) || e.name.toLowerCase().includes(needle);
  const shown = all
    .filter(match)
    .map((e, i) => ({ e, i, first: deviceClass && e.device_class === deviceClass ? 0 : 1 }))
    .sort((a, b) => a.first - b.first || a.i - b.i)
    .slice(0, MAX_SHOWN)
    .map((x) => x.e);

  const note = q.isError
    ? errMessage(q.error)
    : q.data?.truncated
      ? 'The entity list was cut at 1000. Type to search or enter the entity id directly.'
      : undefined;

  return (
    <div
      className="relative"
      onBlur={(ev) => {
        if (!ev.currentTarget.contains(ev.relatedTarget)) setOpen(false);
      }}
    >
      <Field
        label={label}
        value={text}
        placeholder={`${domain}.…`}
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        role="combobox"
        aria-expanded={open && shown.length > 0}
        aria-controls={listId}
        aria-autocomplete="list"
        onFocus={() => setOpen(true)}
        onKeyDown={(ev) => ev.key === 'Escape' && setOpen(false)}
        onChange={(ev) => {
          setText(ev.target.value);
          setOpen(true);
          onChange(ev.target.value);
        }}
        error={q.isError ? note : undefined}
        hint={q.isError ? hint : (note ?? hint)}
      />
      {open && shown.length > 0 && (
        <ul
          id={listId}
          role="listbox"
          aria-label={`${label} suggestions`}
          className="absolute z-40 mt-1 max-h-64 w-full overflow-y-auto rounded-control border border-line bg-panel shadow-lg"
        >
          {shown.map((e) => (
            <li
              key={e.entity_id}
              role="option"
              aria-selected={e.entity_id === value}
              tabIndex={-1}
              className="flex min-h-touch cursor-pointer items-center px-3 py-2 text-sm hover:bg-raised aria-selected:text-accent"
              onMouseDown={(ev) => ev.preventDefault()}
              onClick={() => {
                setText(e.entity_id);
                setOpen(false);
                onChange(e.entity_id);
              }}
            >
              {`${e.name} (${e.entity_id})`}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
