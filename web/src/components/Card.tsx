import type { HTMLAttributes } from 'react';

export function Card({ className = '', ...rest }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={`rounded-panel border border-line bg-panel p-5 ${className}`}
      {...rest}
    />
  );
}
