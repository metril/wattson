import { useEffect, useRef, useState } from 'react';
import { getKioskToken, notifyUnauthorized } from '@/lib/api';
import { photoAt, upcoming, type Photo } from '@/lib/photo-select';
import type { TickStore } from '@/lib/tick-store';

const FADE_MS = 600;
const MAX_URLS = 6;
const DEFAULT_CREATOR = 'Mapillary contributors';

type Loaded = { url: string; creator: string };
type Shown = { id: string; creator: string; capturedAt: number };

function creatorOf(header: string | null): string {
  if (!header) return DEFAULT_CREATOR;
  try {
    return decodeURIComponent(header) || DEFAULT_CREATOR;
  } catch {
    return header;
  }
}

async function fetchPhoto(id: string): Promise<Loaded> {
  const tok = getKioskToken();
  const res = await fetch(`/api/mapillary/images/${encodeURIComponent(id)}`, tok ? { headers: { Authorization: `Bearer ${tok}` } } : undefined);
  if (res.status === 401) notifyUnauthorized(location.pathname + location.search); // revoked kiosk token or expired session
  if (!res.ok) throw new Error(`photo ${res.status}`);
  const creator = creatorOf(res.headers.get('X-Photo-Creator'));
  const blob = await res.blob();
  return { url: URL.createObjectURL(blob), creator };
}

function month(ms: number): string {
  return new Date(ms).toLocaleDateString('en', { month: 'short', year: 'numeric', timeZone: 'UTC' });
}

/**
 * The street photo for the rider's distance, crossfading between two stacked
 * images. Photos go through the server proxy as blobs (so the kiosk bearer
 * applies), at most six object URLs stay alive, and ticks are checked in rAF:
 * React re-renders only when the shown photo (or the panel message) changes.
 */
export default function PhotoView({
  photos,
  ticks,
  reducedMotion,
}: {
  routeId: number;
  photos: Photo[];
  ticks: TickStore;
  reducedMotion: boolean;
}) {
  const layerA = useRef<HTMLImageElement>(null);
  const layerB = useRef<HTMLImageElement>(null);
  const reduced = useRef(reducedMotion);
  const [shown, setShown] = useState<Shown | null>(null);
  const [none, setNone] = useState(false);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    reduced.current = reducedMotion;
    for (const el of [layerA.current, layerB.current]) {
      if (el) el.style.transition = reducedMotion ? '' : `opacity ${FADE_MS}ms linear`;
    }
  }, [reducedMotion]);

  useEffect(() => {
    const layers = [layerA.current!, layerB.current!];
    for (const el of layers) el.style.opacity = '0';
    const ids: (string | null)[] = [null, null];
    const loads = new Map<string, Promise<Loaded>>();
    const ready = new Map<string, Loaded>(); // insertion order = age
    let front = -1;
    let pending: string | null = null; // being decoded: its URL must outlive any trim
    let wanted = -2;
    let token = 0;
    let alive = true;
    let raf = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let finish: (() => void) | null = null;

    const trim = () => {
      for (const [id, l] of ready) {
        if (ready.size <= MAX_URLS) break;
        if (ids.includes(id) || id === pending || id === photos[wanted]?.id) continue;
        URL.revokeObjectURL(l.url);
        ready.delete(id);
        loads.delete(id);
      }
    };
    const load = (p: Photo): Promise<Loaded> => {
      let e = loads.get(p.id);
      if (!e) {
        e = fetchPhoto(p.id).then((l) => {
          if (!alive) {
            URL.revokeObjectURL(l.url);
            return l;
          }
          ready.set(p.id, l);
          trim();
          return l;
        });
        loads.set(p.id, e);
        const mine = e;
        e.catch(() => {
          if (loads.get(p.id) === mine) loads.delete(p.id);
        });
      }
      return e;
    };

    const present = async (p: Photo, l: Loaded, my: number) => {
      finish?.();
      if (front >= 0 && ids[front] === p.id) {
        setShown((s) => (s && s.id === p.id ? s : { id: p.id, creator: l.creator, capturedAt: p.captured_at }));
        return;
      }
      const back = front === 0 ? 1 : 0;
      const img = layers[back];
      pending = p.id;
      try {
        img.src = l.url;
        if (typeof img.decode === 'function') await img.decode();
      } finally {
        if (pending === p.id) pending = null;
      }
      if (!alive || my !== token) return;
      ids[back] = p.id;
      img.style.zIndex = '2';
      if (front >= 0) layers[front].style.zIndex = '1';
      img.style.opacity = '1';
      setShown({ id: p.id, creator: l.creator, capturedAt: p.captured_at });
      const old = front;
      front = back;
      finish = () => {
        clearTimeout(timer);
        finish = null;
        if (old >= 0) {
          layers[old].style.opacity = '0';
          layers[old].removeAttribute('src');
          ids[old] = null;
        }
        trim();
      };
      if (reduced.current || old < 0) finish();
      else timer = setTimeout(() => finish?.(), FADE_MS);
    };

    const check = async () => {
      raf = 0;
      const t = ticks.latest;
      if (!t) return;
      const idx = photoAt(photos, t.distance_m);
      if (idx === wanted) return;
      wanted = idx;
      const my = ++token;
      if (idx < 0) {
        setNone(true);
        return;
      }
      setNone(false);
      for (const n of upcoming(photos, idx)) load(n).catch(() => {});
      try {
        const l = await load(photos[idx]);
        if (!alive || my !== token) return;
        await present(photos[idx], l, my);
        if (alive && my === token) setFailed(false);
      } catch {
        if (alive && my === token) setFailed(true);
      }
    };

    void check();
    const unsub = ticks.subscribe(() => {
      if (!raf) raf = requestAnimationFrame(() => void check());
    });
    return () => {
      alive = false;
      unsub();
      if (raf) cancelAnimationFrame(raf);
      clearTimeout(timer);
      for (const l of ready.values()) URL.revokeObjectURL(l.url);
      ready.clear();
      loads.clear();
    };
  }, [photos, ticks]);

  return (
    <div
      data-testid="photo-view"
      data-photo-id={shown?.id}
      role="group"
      aria-label="Street photo"
      className="ride-viewbox relative overflow-hidden rounded-panel border border-line bg-panel"
    >
      <img ref={layerA} alt="" className="absolute inset-0 size-full object-cover" />
      <img ref={layerB} alt="" className="absolute inset-0 size-full object-cover" />
      {none && (
        <p role="status" className="absolute inset-0 z-10 flex items-center justify-center bg-panel px-4 text-center text-chalk">
          No street photo for this stretch
        </p>
      )}
      {failed && !none && (
        <p role="status" className="absolute left-2 top-2 z-10 rounded-control bg-ground/80 px-3 py-1 text-sm text-chalk">
          Street photos unavailable right now
        </p>
      )}
      {shown && !none && (
        <p className="absolute bottom-2 left-2 z-10 flex max-w-[calc(100%-1rem)] flex-wrap items-center rounded-control bg-ground/80 px-3 text-sm text-chalk">
          <span>
            Photo © {shown.creator} on{' '}
            <a
              href={`https://www.mapillary.com/app/?pKey=${encodeURIComponent(shown.id)}`}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex min-h-touch min-w-touch items-center underline"
            >
              Mapillary
            </a>
            , CC BY-SA 4.0 · {month(shown.capturedAt)}
          </span>
        </p>
      )}
    </div>
  );
}
