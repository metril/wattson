import { useEffect, useState } from 'react';
import { ExternalLink, Upload } from 'lucide-react';
import { useQueryClient } from '@tanstack/react-query';
import { api, ApiError, unwrap } from '@/lib/api';
import { errMessage } from '@/lib/errors';
import type { components } from '@/lib/openapi';
import { Banner } from '@/components/Banner';
import { Button } from '@/components/Button';
import { RIDES_KEY } from '@/pages/History';

type Status = components['schemas']['StravaStatusOut'];

const POLL_MS = 2000;
// About five minutes; Strava normally finishes in seconds.
const MAX_POLLS = 150;

/** Uploads a ride to Strava and follows the background upload until it is done or fails. */
export function StravaUploadButton({ rideId, initial }: { rideId: number; initial: Status }) {
  const qc = useQueryClient();
  const [st, setSt] = useState<Status>(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (st.status !== 'uploading') return;
    let dead = false;
    let timer: ReturnType<typeof setTimeout>;
    let polls = 0;
    const schedule = () => {
      timer = setTimeout(async () => {
        try {
          const s = unwrap(await api.GET('/api/rides/{id}/strava', { params: { path: { id: rideId } } }));
          if (dead) return;
          if (s.status !== 'uploading') {
            setSt(s);
            if (s.status === 'done') void qc.invalidateQueries({ queryKey: RIDES_KEY });
            return;
          }
        } catch (e) {
          if (dead) return;
          // A dropped connection is retried; a definite answer from the server is not.
          if (e instanceof ApiError) {
            setError(errMessage(e));
            return;
          }
        }
        if (++polls >= MAX_POLLS) {
          setError('Still uploading. Check this ride again later.');
          return;
        }
        schedule();
      }, POLL_MS);
    };
    schedule();
    return () => {
      dead = true;
      clearTimeout(timer);
    };
  }, [st.status, rideId, qc]);

  async function start() {
    setBusy(true);
    setError(null);
    try {
      const s = unwrap(await api.POST('/api/rides/{id}/strava', { params: { path: { id: rideId } } }));
      setSt(s);
      if (s.status === 'done') void qc.invalidateQueries({ queryKey: RIDES_KEY });
    } catch (e) {
      setError(
        e instanceof ApiError && e.code === 'strava_not_connected'
          ? 'Connect Strava for this rider on the Riders page.'
          : errMessage(e),
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-2">
      {st.status === 'done' ? (
        <a
          href={st.activity_url}
          target="_blank"
          rel="noopener"
          className="inline-flex min-h-touch min-w-touch items-center justify-center gap-2 rounded-control border border-ok bg-raised px-4 font-medium text-chalk hover:brightness-110"
        >
          <ExternalLink aria-hidden className="size-5" />
          View on Strava
        </a>
      ) : st.status === 'uploading' ? (
        <Button disabled>
          <Upload aria-hidden className="size-5" />
          Uploading…
        </Button>
      ) : (
        <Button disabled={busy} onClick={() => void start()}>
          <Upload aria-hidden className="size-5" />
          {st.status === 'error' ? 'Retry' : 'Upload to Strava'}
        </Button>
      )}
      {st.status === 'error' && !error && <Banner tone="error">{st.error || 'The Strava upload failed.'}</Banner>}
      {error && <Banner tone="error">{error}</Banner>}
    </div>
  );
}
