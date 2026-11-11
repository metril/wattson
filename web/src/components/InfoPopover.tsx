import type { ReactNode } from 'react';
import * as P from '@radix-ui/react-popover';
import { Info } from 'lucide-react';

/** An info button that opens a popover with explanatory content. */
export function InfoPopover({ label, children }: { label: string; children: ReactNode }) {
  return (
    <P.Root>
      <P.Trigger
        data-nav
        aria-label={label}
        className="inline-flex size-12 items-center justify-center rounded-full text-dim hover:bg-raised hover:text-chalk"
      >
        <Info aria-hidden className="size-4" />
      </P.Trigger>
      <P.Portal>
        <P.Content
          aria-label={label}
          align="end"
          sideOffset={4}
          collisionPadding={12}
          className="z-50 w-[min(22rem,calc(100vw-1.5rem))] rounded-panel bg-overlay p-4 text-sm text-chalk shadow-panel ring-1 ring-line-soft"
        >
          {children}
          <P.Arrow className="fill-overlay" />
        </P.Content>
      </P.Portal>
    </P.Root>
  );
}
