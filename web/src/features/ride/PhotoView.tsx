import { useEffect, useRef, useState } from 'react';
import { getKioskToken, notifyUnauthorized } from '@/lib/api';
import { createLook, attachLookInput, wrap180, type Look } from '@/lib/look';
import { photoAt, upcoming, type Photo } from '@/lib/photo-select';
import type { TickStore } from '@/lib/tick-store';

const FADE_MS = 600;
const MAX_URLS = 6;
const PANO_HFOV = 110; // degrees of the sphere across the container width
const PANO_MAX_VFOV = 70; // never show more than this much height (keeps sky and hood out)
const PANO_TILT = 8; // view centre this many degrees above the horizon
type Loaded = { url: string };
type Shown = { id: string; pano: boolean };

/**
 * Forward crop of an equirectangular panorama as CSS background styles: the
 * horizontal centre is the riding direction (the image centre is the camera
 * heading), scaled so ~110 degrees span the width and at most 70 the height.
 * yawDeg turns the view clockwise from forward (the look-around offset); the crop wraps at the seam.
 * Container query units need the parent to be a size container.
 */
export function panoCrop(p: Pick<Photo, 'bearing_deg' | 'route_bearing_deg'>, yawDeg = 0) {
  const col = (((0.5 + wrap180(p.route_bearing_deg + yawDeg - p.bearing_deg) / 360) % 1) + 1) % 1;
  const row = 0.5 - PANO_TILT / 180;
  const w = `max(${+(36000 / PANO_HFOV).toFixed(3)}cqw, ${+(36000 / PANO_MAX_VFOV).toFixed(3)}cqh)`;
  return {
    col,
    row,
    backgroundSize: `${w} auto`,
    backgroundPosition: `calc(50cqw - ${col} * ${w}) calc(50cqh - ${row} * ${w} / 2)`,
  };
}

function applyView(img: HTMLImageElement, p: Photo, url: string) {
  const st = img.style;
  if (p.pano) {
    const c = panoCrop(p);
    // The <img> still decodes and fades; its own pixels are pushed out of the box and the
    // crop is painted as a repeat-x background, which wraps at the seam.
    st.objectFit = 'none';
    st.objectPosition = '-99999px -99999px';
    st.backgroundImage = `url("${url}")`;
    st.backgroundRepeat = 'repeat-x';
    st.backgroundSize = c.backgroundSize;
    st.backgroundPosition = c.backgroundPosition;
    img.dataset.pano = 'true';
  } else {
    st.objectFit = st.objectPosition = st.backgroundImage = st.backgroundRepeat = st.backgroundSize = st.backgroundPosition = '';
    delete img.dataset.pano;
  }
}

async function fetchPhoto(id: string, onUnauthorized: () => void): Promise<Loaded> {
  const tok = getKioskToken();
  const res = await fetch(`/api/mapillary/images/${encodeURIComponent(id)}`, tok ? { headers: { Authorization: `Bearer ${tok}` } } : undefined);
  if (res.status === 401) onUnauthorized(); // revoked kiosk token or expired session
  if (!res.ok) throw new Error(`photo ${res.status}`);
  const blob = await res.blob();
  return { url: URL.createObjectURL(blob) };
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
  fill,
  lookable = true,
}: {
  photos: Photo[];
  ticks: TickStore;
  reducedMotion: boolean;
  /** Fill the parent instead of using the ride view height. */
  fill?: boolean;
  /** Focusable and turnable (panoramas). False for the small inset under the swap button. */
  lookable?: boolean;
}) {
  const lookableRef = useRef(lookable);
  lookableRef.current = lookable;
  const layerA = useRef<HTMLImageElement>(null);
  const layerB = useRef<HTMLImageElement>(null);
  const reduced = useRef(reducedMotion);
  const root = useRef<HTMLDivElement>(null);
  const panos = useRef<(Photo | null)[]>([null, null]); // the panorama on each layer, to re-crop on look-around
  const frontPano = useRef(false);
  const look = useRef<Look | null>(null);
  const [shown, setShown] = useState<Shown | null>(null);
  const [none, setNone] = useState(false);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (!lookable) look.current?.reset();
  }, [lookable]);

  useEffect(() => {
    reduced.current = reducedMotion;
    for (const el of [layerA.current, layerB.current]) {
      if (el) el.style.transition = reducedMotion ? '' : `opacity ${FADE_MS}ms linear`;
    }
  }, [reducedMotion]);

  // Look-around: drag or Left/Right while this view has focus (panoramas only); eases back to forward.
  useEffect(() => {
    const layers = [layerA.current!, layerB.current!];
    const l = createLook({
      apply: (yaw) => {
        root.current!.dataset.yaw = yaw.toFixed(1);
        panos.current.forEach((p, i) => {
          if (p) layers[i].style.backgroundPosition = panoCrop(p, yaw).backgroundPosition;
        });
      },
      reduced: () => reduced.current,
    });
    look.current = l;
    const off = attachLookInput(root.current!, l, { enabled: () => frontPano.current && lookableRef.current, fovDeg: PANO_HFOV });
    return () => {
      off();
      l.dispose();
      look.current = null;
    };
  }, []);

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
    // A revoked token fails the shown photo and its prefetches together: tell the app once per burst.
    let unauthorized = false;
    const onUnauthorized = () => {
      if (unauthorized) return;
      unauthorized = true;
      notifyUnauthorized(location.pathname + location.search);
    };

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
        e = fetchPhoto(p.id, onUnauthorized).then((l) => {
          unauthorized = false;
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
        setShown((s) => (s && s.id === p.id ? s : { id: p.id, pano: !!p.pano }));
        return;
      }
      const back = front === 0 ? 1 : 0;
      const img = layers[back];
      pending = p.id;
      try {
        panos.current[back] = p.pano ? p : null;
        applyView(img, p, l.url);
        img.src = l.url;
        if (typeof img.decode === 'function') await img.decode();
      } finally {
        if (pending === p.id) pending = null;
      }
      if (!alive || my !== token) return;
      ids[back] = p.id;
      frontPano.current = !!p.pano;
      look.current?.reset(); // a new photo looks forward
      img.style.zIndex = '2';
      if (front >= 0) layers[front].style.zIndex = '1';
      img.style.opacity = '1';
      setShown({ id: p.id, pano: !!p.pano });
      const old = front;
      front = back;
      finish = () => {
        clearTimeout(timer);
        finish = null;
        if (old >= 0) {
          layers[old].style.opacity = '0';
          layers[old].removeAttribute('src');
          ids[old] = null;
          panos.current[old] = null;
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
      ref={root}
      data-testid="photo-view"
      data-photo-id={shown?.id}
      data-nav={lookable ? '' : undefined}
      tabIndex={lookable ? 0 : undefined}
      style={{ containerType: 'size', touchAction: 'pan-y' }}
      role="group"
      aria-label={shown?.pano && lookable ? 'Street photo, 360 degree panorama. Press Left or Right arrow, or drag, to look around.' : 'Street photo'}
      className={`${fill ? 'size-full' : 'ride-viewbox'} relative overflow-hidden rounded-panel border border-line bg-panel focus-visible:outline-2 focus-visible:outline-accent`}
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
    </div>
  );
}
