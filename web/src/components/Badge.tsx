import type { ReactNode } from 'react';

type Tone = 'neutral' | 'accent' | 'ok' | 'warn' | 'danger' | 'z1' | 'z2' | 'z3' | 'z4' | 'z5' | 'z6' | 'z7';

const tones: Record<Tone, string> = {
  neutral: 'bg-raised text-dim',
  accent: 'bg-accent-soft text-accent',
  ok: 'bg-ok/15 text-ok',
  warn: 'bg-warn/15 text-warn',
  danger: 'bg-danger/15 text-danger',
  z1: 'bg-zone-1/20 text-zone-1',
  z2: 'bg-zone-2/20 text-zone-2',
  z3: 'bg-zone-3/20 text-zone-3',
  z4: 'bg-zone-4/20 text-zone-4',
  z5: 'bg-zone-5/20 text-zone-5',
  z6: 'bg-zone-6/20 text-zone-6',
  z7: 'bg-zone-7/20 text-zone-7',
};

/** Small status or category pill. */
export function Badge({ tone = 'neutral', children, className = '' }: { tone?: Tone; children: ReactNode; className?: string }) {
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-medium ${tones[tone]} ${className}`}>
      {children}
    </span>
  );
}
