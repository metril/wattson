export type ViewMode = '2d' | '3d' | 'photo' | 'photo-inset' | 'split';
/** Views that show the street photo and the 2D map together. */
export const isCombined = (v: ViewMode) => v === 'photo-inset' || v === 'split';
/** map is false for synthetic routes (no geography). */
export type Caps = {
  webgl: boolean;
  reducedMotion: boolean;
  photos: boolean;
  map: boolean;
  /** Why photos are unavailable, when the server said (building, error, no coverage). */
  photoNote?: string | null;
};
export type Tier = 'full' | 'lite';
/** Map shown beside the street photo in the combined views. */
export type ComboMap = '2d' | '3d';
export type Prefs = {
  view: ViewMode;
  exaggeration: number;
  tier: Tier;
  /** 2D map turns with the riding direction (false: north up). */
  headingUp: boolean;
  comboMap: ComboMap;
};

const K = {
  view: 'wattson.view',
  exaggeration: 'wattson.view.exaggeration',
  tier: 'wattson.view.tier',
  headingUp: 'wattson.view.headingUp',
  comboMap: 'wattson.view.comboMap',
} as const;

const DEFAULTS: Prefs = { view: '2d', exaggeration: 1.5, tier: 'full', headingUp: false, comboMap: '2d' };

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
    view: v === '3d' || v === 'photo' || v === 'photo-inset' || v === 'split' || v === '2d' ? v : DEFAULTS.view,
    exaggeration: e,
    tier: t === 'lite' || t === 'full' ? t : DEFAULTS.tier,
    headingUp: get(K.headingUp) === '1',
    comboMap: get(K.comboMap) === '3d' ? '3d' : DEFAULTS.comboMap,
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
    if (p.headingUp !== undefined) localStorage.setItem(K.headingUp, p.headingUp ? '1' : '0');
    if (p.comboMap !== undefined) localStorage.setItem(K.comboMap, p.comboMap);
  } catch {
    /* storage blocked: preferences last for this page only */
  }
}

/** The view that can actually be shown; anything unavailable is the 2D map and profile. */
export function effectiveView(want: ViewMode, c: Caps): ViewMode {
  if (want === '3d' && c.webgl && !c.reducedMotion && c.map) return '3d';
  if (want === 'photo' && c.photos) return 'photo';
  if (isCombined(want) && c.photos && c.map) return want;
  return '2d';
}

/** The map a combined view really shows: 3D only where 3D can run. */
export function effectiveComboMap(want: ComboMap, c: Caps): ComboMap {
  return want === '3d' && c.webgl && !c.reducedMotion ? '3d' : '2d';
}

export function unavailableReason(v: ViewMode, c: Caps): string | null {
  if (v === '3d') {
    if (!c.webgl) return "3D isn't supported on this display";
    if (c.reducedMotion) return 'Off because reduced motion is on';
    if (!c.map) return '3D needs a route with a map';
  }
  if ((v === 'photo' || isCombined(v)) && !c.photos) return c.photoNote || 'No street photos for this route';
  return null;
}

export function detectTier(nav: { hardwareConcurrency?: number; deviceMemory?: number }): Tier {
  const cores = nav.hardwareConcurrency;
  const mem = nav.deviceMemory;
  if (cores !== undefined && cores <= 4) return 'lite';
  if (mem !== undefined && mem <= 4) return 'lite';
  return 'full';
}
