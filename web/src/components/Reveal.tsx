import type { ReactNode } from 'react';
import { LazyMotion, domAnimation } from 'motion/react';
import * as m from 'motion/react-m';

/** Fades and lifts its children in on mount; `delay` staggers siblings. Honours reduced motion. */
export function Reveal({ children, delay = 0, className = '' }: { children: ReactNode; delay?: number; className?: string }) {
  return (
    <LazyMotion features={domAnimation} strict>
      <m.div
        className={className}
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.35, delay, ease: 'easeOut' }}
      >
        {children}
      </m.div>
    </LazyMotion>
  );
}
