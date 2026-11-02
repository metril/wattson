import { useEffect, useId, useRef, useState } from 'react';
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react';
import { Button } from '@/components/Button';
import { Field } from '@/components/Field';
import {
  defaultBlock,
  MAX_BLOCKS,
  mmss,
  parseDuration,
  resolve,
  type Block,
  type RiderLite,
  type WorkoutType,
} from '@/lib/workout-math';

type Kind = 'level' | 'level_offset' | 'power_w' | 'ftp_pct';

const KINDS: Record<WorkoutType, { kind: Kind; label: string; def: number }[]> = {
  interval: [
    { kind: 'level', label: 'Level', def: 5 },
    { kind: 'level_offset', label: 'Offset', def: 0 },
  ],
  erg: [
    { kind: 'power_w', label: 'Watts', def: 150 },
    { kind: 'ftp_pct', label: '% FTP', def: 60 },
  ],
};

const ALL_KEYS: Kind[] = ['level', 'level_offset', 'power_w', 'ftp_pct'];

const kindOf = (b: Block): Kind | undefined => ALL_KEYS.find((k) => b[k] !== undefined);

/** The block with its target replaced; other targets are dropped so no stray key reaches the API. */
function withTarget(b: Block, kind: Kind, n: number | undefined): Block {
  const next: Block = { ...b };
  for (const k of ALL_KEYS) delete next[k];
  if (n !== undefined) next[kind] = n;
  return next;
}

/** Sets or clears an optional text key. */
function withText(b: Block, k: 'cadence' | 'label', v: string): Block {
  const next: Block = { ...b };
  if (v === '') delete next[k];
  else next[k] = v;
  return next;
}

/** m:ss text input that holds partial typing ("1:") without rewriting it. */
function DurationField({ label, value, error, onChange }: { label: string; value: number; error?: string; onChange(s: number): void }) {
  const [draft, setDraft] = useState(mmss(value));
  const [unparsed, setUnparsed] = useState(false);
  useEffect(() => {
    // An outside change (move, remove, type switch) replaces the text; our own typing does not.
    setDraft((d) => (parseDuration(d) === value ? d : mmss(value)));
    setUnparsed(false);
  }, [value]);
  return (
    <Field
      label={label}
      value={draft}
      inputMode="numeric"
      autoComplete="off"
      placeholder="m:ss"
      error={unparsed ? 'Use m:ss, for example 1:30.' : error}
      onChange={(e) => {
        const v = e.target.value;
        setDraft(v);
        const s = parseDuration(v);
        setUnparsed(s === null);
        if (s !== null) onChange(s);
      }}
      onBlur={() => {
        if (parseDuration(draft) === null) {
          setDraft(mmss(value));
          setUnparsed(false);
        }
      }}
    />
  );
}

function Row(p: {
  id: number;
  n: number;
  count: number;
  type: WorkoutType;
  block: Block;
  rider?: RiderLite;
  errors: Record<string, string>;
  onChange(b: Block): void;
  onMove(d: -1 | 1): void;
  onRemove(): void;
}) {
  const id = useId();
  const { n, block: b, type } = p;
  const opts = KINDS[type];
  const derived = kindOf(b);
  // Keep the chosen kind while its number is cleared.
  const [mem, setMem] = useState<Kind>(derived ?? opts[0].kind);
  useEffect(() => {
    if (derived) setMem(derived);
  }, [derived]);
  const kind = derived ?? (opts.some((o) => o.kind === mem) ? mem : opts[0].kind);
  const err = (f: string) => p.errors[`blocks[${n}].${f}`];
  const targetErr = ALL_KEYS.map(err).find(Boolean);
  const name = `Block ${n + 1}`;
  const resolved = p.rider && b[kind] !== undefined ? resolve(type, [b], p.rider)[0].value : undefined;
  const hint =
    resolved === undefined ? undefined : kind === 'level_offset' ? `Level ${resolved} for this rider` : kind === 'ftp_pct' ? `${resolved} W for this rider` : undefined;

  return (
    <li aria-label={name} data-block-id={p.id} className="flex flex-col gap-3 rounded-control border border-line bg-panel p-3">
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-[8rem_9rem_9rem_1fr_1fr]">
        <DurationField
          label={`${name} duration`}
          value={b.duration_s}
          error={err('duration_s')}
          onChange={(s) => p.onChange({ ...b, duration_s: s })}
        />
        <div className="flex flex-col gap-1.5">
          <label htmlFor={`${id}-kind`} className="text-sm font-medium text-dim">
            {name} target
          </label>
          <select
            id={`${id}-kind`}
            value={kind}
            className="min-h-touch w-full rounded-control border border-line bg-raised px-3 text-chalk"
            onChange={(e) => {
              const k = e.target.value as Kind;
              setMem(k);
              p.onChange(withTarget(b, k, opts.find((o) => o.kind === k)?.def));
            }}
          >
            {opts.map((o) => (
              <option key={o.kind} value={o.kind}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
        <Field
          label={`${name} ${opts.find((o) => o.kind === kind)?.label} value`}
          type="number"
          inputMode="numeric"
          step={1}
          value={b[kind] ?? ''}
          hint={hint}
          error={targetErr}
          onChange={(e) => p.onChange(withTarget(b, kind, e.target.value === '' ? undefined : Number(e.target.value)))}
        />
        <Field
          label={`${name} cadence`}
          value={b.cadence ?? ''}
          placeholder="rpm, e.g. 90-100"
          error={err('cadence')}
          onChange={(e) => p.onChange(withText(b, 'cadence', e.target.value))}
        />
        <Field
          label={`${name} label`}
          value={b.label ?? ''}
          placeholder="e.g. Warm-up"
          error={err('label')}
          onChange={(e) => p.onChange(withText(b, 'label', e.target.value))}
        />
      </div>
      <div className="flex flex-wrap gap-2">
        <Button data-act="up" disabled={n === 0} onClick={() => p.onMove(-1)}>
          <ArrowUp aria-hidden className="size-5" />
          Move up<span className="sr-only"> block {n + 1}</span>
        </Button>
        <Button data-act="down" disabled={n === p.count - 1} onClick={() => p.onMove(1)}>
          <ArrowDown aria-hidden className="size-5" />
          Move down<span className="sr-only"> block {n + 1}</span>
        </Button>
        <Button data-act="remove" disabled={p.count === 1} onClick={p.onRemove}>
          <Trash2 aria-hidden className="size-5" />
          Remove<span className="sr-only"> block {n + 1}</span>
        </Button>
      </div>
    </li>
  );
}

/**
 * One row per block, with add, reorder (buttons, no drag) and remove. `rider` only adds the "= 150 W" hints.
 * Rows are keyed by client-only ids (never sent to the API) so focus and typing state follow a block when it moves.
 */
export function BlockEditor(p: {
  type: WorkoutType;
  blocks: Block[];
  onChange(bs: Block[]): void;
  errors: Record<string, string>;
  rider?: RiderLite;
}) {
  const list = useRef<HTMLOListElement>(null);
  const addBtn = useRef<HTMLButtonElement>(null);
  const nextId = useRef(0);
  const ids = useRef<number[]>([]);
  // Blocks changed from outside (load, type switch) keep their order; track only how many there are.
  while (ids.current.length < p.blocks.length) ids.current.push(nextId.current++);
  if (ids.current.length > p.blocks.length) ids.current.length = p.blocks.length;

  const [note, setNote] = useState('');
  // What to focus once the re-ordered rows are on screen.
  const focus = useRef<{ id: number; act: 'up' | 'down' } | { removedAt: number } | null>(null);
  useEffect(() => {
    const f = focus.current;
    focus.current = null;
    if (!f) return;
    const q = (sel: string) => list.current?.querySelector<HTMLElement>(sel) ?? null;
    const usable = (el: HTMLElement | null) => (el && !(el as HTMLButtonElement).disabled ? el : null);
    let target: HTMLElement | null;
    if ('id' in f) {
      const row = `[data-block-id="${f.id}"]`;
      const other = f.act === 'up' ? 'down' : 'up';
      target = usable(q(`${row} [data-act="${f.act}"]`)) ?? usable(q(`${row} [data-act="${other}"]`));
    } else {
      const rows = list.current?.querySelectorAll<HTMLElement>('li') ?? [];
      const row = rows[Math.min(f.removedAt, rows.length - 1)];
      target = usable(row?.querySelector<HTMLElement>('[data-act="remove"]') ?? null) ?? addBtn.current;
    }
    target?.focus();
  }, [p.blocks]);

  const set = (i: number, b: Block) => p.onChange(p.blocks.map((x, j) => (j === i ? b : x)));
  const move = (i: number, d: -1 | 1) => {
    const bs = [...p.blocks];
    [bs[i], bs[i + d]] = [bs[i + d], bs[i]];
    const k = [...ids.current];
    [k[i], k[i + d]] = [k[i + d], k[i]];
    focus.current = { id: ids.current[i], act: d === 1 ? 'down' : 'up' };
    ids.current = k;
    setNote(`Block ${i + 1} moved to position ${i + d + 1}`);
    p.onChange(bs);
  };
  const remove = (i: number) => {
    ids.current = ids.current.filter((_, j) => j !== i);
    focus.current = { removedAt: i };
    setNote(`Block ${i + 1} removed`);
    p.onChange(p.blocks.filter((_, j) => j !== i));
  };
  return (
    <div className="flex flex-col gap-3">
      <ol ref={list} className="flex flex-col gap-3">
        {p.blocks.map((b, i) => (
          <Row
            key={ids.current[i]}
            id={ids.current[i]}
            n={i}
            count={p.blocks.length}
            type={p.type}
            block={b}
            rider={p.rider}
            errors={p.errors}
            onChange={(nb) => set(i, nb)}
            onMove={(d) => move(i, d)}
            onRemove={() => remove(i)}
          />
        ))}
      </ol>
      <div className="sr-only" aria-live="polite" aria-atomic="true">
        {note}
      </div>
      {p.errors.blocks && (
        <p role="alert" className="text-sm text-danger">
          {p.errors.blocks}
        </p>
      )}
      <div>
        <Button
          ref={addBtn}
          disabled={p.blocks.length >= MAX_BLOCKS}
          onClick={() => {
            ids.current = [...ids.current, nextId.current++];
            setNote(`Block ${p.blocks.length + 1} added`);
            p.onChange([...p.blocks, defaultBlock(p.type)]);
          }}
        >
          <Plus aria-hidden className="size-5" />
          Add block
        </Button>
      </div>
    </div>
  );
}
