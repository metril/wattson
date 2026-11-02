import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { getKioskToken, setKioskToken } from '@/lib/api';
import { ME_KEY } from '@/lib/auth';

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div className="mx-auto flex min-h-dvh max-w-md flex-col justify-center gap-4 p-4">
      <h1 className="numeral text-2xl">Kiosk</h1>
      {children}
    </div>
  );
}

/**
 * Pairing landing page. The token travels only in the URL fragment (never sent
 * to the server); it is stored, scrubbed from the address bar, then the ride
 * screen opens. A display that already holds a token asks before replacing it.
 */
export function KioskPage() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  // Read once: the fragment is scrubbed right away.
  const [fragment] = useState(() => new URLSearchParams(window.location.hash.replace(/^#/, '')).get('token'));
  const [stored] = useState(getKioskToken);
  const valid = !!fragment && fragment.startsWith('wat_');
  const needsConfirm = valid && !!stored && stored !== fragment;

  useEffect(() => {
    if (fragment) window.history.replaceState(null, '', '/kiosk');
    if (needsConfirm) return;
    if (valid) {
      setKioskToken(fragment);
      void qc.invalidateQueries({ queryKey: ME_KEY });
    }
    if (valid || (!fragment && stored)) navigate('/', { replace: true });
  }, [fragment, valid, needsConfirm, stored, navigate, qc]);

  if (fragment && !valid) {
    return (
      <Shell>
        <Banner tone="error">This pairing link is not valid. Create a new one in Settings → Tokens.</Banner>
      </Shell>
    );
  }
  if (needsConfirm) {
    return (
      <Shell>
        <Card className="flex flex-col gap-4">
          <p>This display is already paired. Replace its pairing with the new one?</p>
          <div className="flex flex-wrap gap-3">
            <Button
              variant="primary"
              onClick={() => {
                setKioskToken(fragment);
                void qc.invalidateQueries({ queryKey: ME_KEY });
                navigate('/', { replace: true });
              }}
            >
              Replace pairing
            </Button>
            <Button onClick={() => navigate('/', { replace: true })}>Keep current</Button>
          </div>
        </Card>
      </Shell>
    );
  }
  if (valid || stored) return null;
  return (
    <Shell>
      <Card className="text-dim">Scan the pairing QR code from Settings → Tokens</Card>
    </Shell>
  );
}
