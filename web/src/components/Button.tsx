import { forwardRef, type ButtonHTMLAttributes } from 'react';

type Variant = 'primary' | 'secondary' | 'danger';

const variants: Record<Variant, string> = {
  primary: 'bg-accent text-accent-ink hover:brightness-110',
  secondary: 'border border-line-soft bg-raised shadow-sm text-chalk hover:border-accent',
  danger: 'bg-danger text-danger-ink hover:brightness-110',
};

/** Touch-sized button (48 px). Defaults to type="button". */
export const Button = forwardRef<HTMLButtonElement, ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant }>(function Button(
  { variant = 'secondary', className = '', type = 'button', ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      data-nav
      className={`inline-flex min-h-touch min-w-touch items-center justify-center gap-2 rounded-control px-4 font-medium disabled:cursor-not-allowed disabled:opacity-50 ${variants[variant]} ${className}`}
      {...rest}
    />
  );
});
