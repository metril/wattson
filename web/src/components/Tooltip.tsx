import type { ReactNode } from 'react';
import * as T from '@radix-ui/react-tooltip';

/** Hover/focus hint around a single focusable child. */
export function Tooltip({ content, children }: { content: ReactNode; children: ReactNode }) {
  return (
    <T.Provider delayDuration={200}>
      <T.Root>
        <T.Trigger asChild>{children}</T.Trigger>
        <T.Portal>
          <T.Content
            sideOffset={6}
            className="z-50 max-w-xs rounded-control bg-overlay px-3 py-1.5 text-xs text-chalk shadow-panel ring-1 ring-line-soft"
          >
            {content}
          </T.Content>
        </T.Portal>
      </T.Root>
    </T.Provider>
  );
}
