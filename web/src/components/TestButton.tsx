import { useState } from 'react';
import { CircleCheck, Loader2, OctagonAlert } from 'lucide-react';
import type { components } from '@/lib/openapi';
import { errMessage } from '@/lib/errors';
import { Button } from './Button';

type Result = components['schemas']['TestResult'];

/** Runs a connection test and shows spinner, then success (status) or failure (alert) with latency. */
export function TestButton({
  run,
  label = 'Test',
  disabled,
}: {
  run: () => Promise<Result>;
  label?: string;
  disabled?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<Result | null>(null);

  async function go() {
    setBusy(true);
    setResult(null);
    try {
      setResult(await run());
    } catch (e) {
      setResult({ ok: false, message: errMessage(e), latency_ms: 0 });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-3">
      <Button onClick={go} disabled={busy || disabled}>
        {busy && <Loader2 aria-hidden className="size-5 animate-spin" />}
        {label}
      </Button>
      {result && (
        <p
          role={result.ok ? 'status' : 'alert'}
          className={`flex min-w-0 items-center gap-2 text-sm ${result.ok ? 'text-ok' : 'text-danger'}`}
        >
          {result.ok ? (
            <CircleCheck aria-hidden className="size-5 shrink-0" />
          ) : (
            <OctagonAlert aria-hidden className="size-5 shrink-0" />
          )}
          <span className="min-w-0 break-words">
            {result.message}
            {result.latency_ms > 0 && ` · ${result.latency_ms} ms`}
          </span>
        </p>
      )}
    </div>
  );
}
