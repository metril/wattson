import { useEffect, useRef, useState } from 'react';
import { Check, Copy } from 'lucide-react';
import { copyText } from '@/lib/clipboard';
import { Button } from '@/components/Button';

/** Read-only redirect (callback) URI with a Copy button; register it at the identity provider. */
export function RedirectUri({
  uri,
  name,
  label = 'Redirect URI',
}: {
  uri: string;
  name: string;
  label?: string;
}) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const timer = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => () => clearTimeout(timer.current), []);

  async function copy() {
    setState((await copyText(uri)) ? 'copied' : 'failed');
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setState('idle'), 2000);
  }

  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-sm font-medium text-dim">{label}</span>
      <div className="flex items-center gap-2">
        <code className="min-w-0 flex-1 rounded-control border border-line bg-raised px-3 py-3 text-sm break-all select-all">
          {uri}
        </code>
        <Button aria-label={`Copy redirect URI for ${name}`} onClick={copy}>
          {state === 'copied' ? <Check aria-hidden className="size-5 text-ok" /> : <Copy aria-hidden className="size-5" />}
        </Button>
      </div>
      <p aria-live="polite" className="min-h-4 text-xs text-dim">
        {state === 'copied' ? 'Copied' : state === 'failed' ? 'Copy failed: select the address and copy it by hand.' : ''}
      </p>
    </div>
  );
}
