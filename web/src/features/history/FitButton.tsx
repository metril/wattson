import { useState } from 'react';
import { Download } from 'lucide-react';
import { ApiError, getKioskToken } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';

const linkCls =
  'inline-flex min-h-touch min-w-touch items-center justify-center gap-2 rounded-control border border-line bg-raised px-4 font-medium text-chalk hover:border-accent';

function fileName(header: string | null, id: number): string {
  const m = header && /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(header);
  return m ? decodeURIComponent(m[1]) : `wattson-${id}.fit`;
}

/**
 * Downloads the ride as a FIT file. A browser session uses a plain link; a paired
 * display has only a Bearer token, which a link cannot send, so it fetches the file
 * and saves the blob.
 */
export function FitButton({ rideId }: { rideId: number }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const href = `/api/rides/${rideId}/fit`;
  const token = getKioskToken();

  if (!token) {
    return (
      <a href={href} download className={linkCls}>
        <Download aria-hidden className="size-5" />
        Download FIT
      </a>
    );
  }

  async function save() {
    setBusy(true);
    setError(null);
    let url = '';
    try {
      const r = await fetch(href, { credentials: 'same-origin', headers: { Authorization: `Bearer ${token}` } });
      if (!r.ok) {
        const body = (await r.json().catch(() => null)) as { error?: { code?: string; message?: string } } | null;
        throw new ApiError(r.status, body?.error?.code ?? 'unknown', body?.error?.message ?? `Request failed (${r.status})`);
      }
      const blob = await r.blob();
      url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = fileName(r.headers.get('content-disposition'), rideId);
      document.body.appendChild(a);
      a.click();
      a.remove();
      const u = url;
      setTimeout(() => URL.revokeObjectURL(u), 250);
    } catch (e) {
      if (url) URL.revokeObjectURL(url);
      setError(errMessage(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <Button disabled={busy} onClick={() => void save()}>
        <Download aria-hidden className="size-5" />
        Download FIT
      </Button>
      {error && <Banner tone="error">{error}</Banner>}
    </div>
  );
}
