export type ViewMode = '2d' | '3d' | 'photo';
/** map is false for synthetic routes (no geography). */
export type Caps = { webgl: boolean; reducedMotion: boolean; photos: boolean; map: boolean };
export type Tier = 'full' | 'lite';
export type Prefs = { view: ViewMode; exaggeration: number; tier: Tier; fellBack: boolean };

const K = {
  view: 'wattson.view',
  exaggeration: 'wattson.view.exaggeration',
  tier: 'wattson.view.tier',
  fellBack: 'wattson.view.fell_back',
} as const;

const DEFAULTS: Prefs = { view: '2d', exaggeration: 1.5, tier: 'full', fellBack: false };

function get(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function loadPrefs(): Prefs {
  const v = get(K.view);
  const t = get(K.tier);
  const x = Number(get(K.exaggeration));
  const e = get(K.exaggeration) !== null && Number.isFinite(x) ? Math.min(3, Math.max(1, x)) : DEFAULTS.exaggeration;
  return {
    view: v === '3d' || v === 'photo' || v === '2d' ? v : DEFAULTS.view,
    exaggeration: e,
    tier: t === 'lite' || t === 'full' ? t : DEFAULTS.tier,
    fellBack: get(K.fellBack) === '1',
  };
}

/** True once a tier has been stored (by a fallback), so detectTier is only the first guess. */
export function hasSavedTier(): boolean {
  return get(K.tier) !== null;
}

export function savePrefs(p: Partial<Prefs>): void {
  try {
    if (p.view !== undefined) localStorage.setItem(K.view, p.view);
    if (p.exaggeration !== undefined) localStorage.setItem(K.exaggeration, String(p.exaggeration));
    if (p.tier !== undefined) localStorage.setItem(K.tier, p.tier);
    if (p.fellBack !== undefined) localStorage.setItem(K.fellBack, p.fellBack ? '1' : '0');
  } catch {
    /* storage blocked: preferences last for this page only */
  }
}

/** The view that can actually be shown; anything unavailable is the 2D map and profile. */
export function effectiveView(want: ViewMode, c: Caps): ViewMode {
  if (want === '3d' && c.webgl && !c.reducedMotion && c.map) return '3d';
  if (want === 'photo' && c.photos) return 'photo';
  return '2d';
}

export function unavailableReason(v: ViewMode, c: Caps): string | null {
  if (v === '3d') {
    if (!c.webgl) return "3D isn't supported on this display";
    if (c.reducedMotion) return 'Off because reduced motion is on';
    if (!c.map) return '3D needs a route with a map';
  }
  if (v === 'photo' && !c.photos) return 'No street photos for this route';
  return null;
}

export function detectTier(nav: { hardwareConcurrency?: number; deviceMemory?: number }): Tier {
  const cores = nav.hardwareConcurrency;
  const mem = nav.deviceMemory;
  if (cores !== undefined && cores <= 4) return 'lite';
  if (mem !== undefined && mem <= 4) return 'lite';
  return 'full';
}
