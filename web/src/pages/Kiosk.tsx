import { useEffect } from 'react';
import { useNavigate } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { Card } from '@/components/Card';
import { getKioskToken, setKioskToken } from '@/lib/api';
import { ME_KEY } from '@/lib/auth';

/**
 * Pairing landing page. The token travels only in the URL fragment (never sent
 * to the server); it is stored, scrubbed from the address bar, then the ride
 * screen opens.
 */
export function KioskPage() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const fragment = new URLSearchParams(window.location.hash.replace(/^#/, '')).get('token');
  const paired = !!(fragment || getKioskToken());

  useEffect(() => {
    if (fragment) {
      setKioskToken(fragment);
      window.history.replaceState(null, '', '/kiosk');
      void qc.invalidateQueries({ queryKey: ME_KEY });
    }
    if (fragment || getKioskToken()) navigate('/', { replace: true });
  }, [fragment, navigate, qc]);

  if (paired) return null;
  return (
    <div className="mx-auto flex min-h-dvh max-w-md flex-col justify-center gap-4 p-4">
      <h1 className="numeral text-2xl">Kiosk</h1>
      <Card className="text-dim">Scan the pairing QR code from Settings → Tokens</Card>
    </div>
  );
}
