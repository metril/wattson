import type { ReactNode } from 'react';
import { AlertTriangle, CircleCheck, Info, OctagonAlert } from 'lucide-react';

type Tone = 'info' | 'ok' | 'warn' | 'error';

const tones: Record<Tone, { cls: string; Icon: typeof Info }> = {
  info: { cls: 'border-accent text-chalk', Icon: Info },
  ok: { cls: 'border-ok text-chalk', Icon: CircleCheck },
  warn: { cls: 'border-warn text-chalk', Icon: AlertTriangle },
  error: { cls: 'border-danger text-chalk', Icon: OctagonAlert },
};

const iconColor: Record<Tone, string> = {
  info: 'text-accent',
  ok: 'text-ok',
  warn: 'text-warn',
  error: 'text-danger',
};

/** Inline status message. Errors are announced (role=alert); others are polite. */
export function Banner({
  tone = 'info',
  children,
  action,
}: {
  tone?: Tone;
  children: ReactNode;
  action?: ReactNode;
}) {
  const { cls, Icon } = tones[tone];
  return (
    <div
      role={tone === 'error' ? 'alert' : 'status'}
      className={`flex items-start gap-3 rounded-control border-l-4 bg-raised px-4 py-3 ${cls}`}
    >
      <Icon aria-hidden className={`mt-0.5 size-5 shrink-0 ${iconColor[tone]}`} />
      <div className="min-w-0 flex-1">{children}</div>
      {action}
    </div>
  );
}
