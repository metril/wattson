import { detectTier, effectiveView, hasSavedTier, loadPrefs, savePrefs, unavailableReason, type Caps } from './view-prefs';

const ok: Caps = { webgl: true, reducedMotion: false, photos: true, map: true };

afterEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
});

describe('view prefs', () => {
  it('defaults', () => {
    expect(loadPrefs()).toEqual({ view: '2d', exaggeration: 1.5, tier: 'full' });
    expect(hasSavedTier()).toBe(false);
  });
  it('round trips and clamps exaggeration', () => {
    savePrefs({ view: '3d', exaggeration: 9, tier: 'lite' });
    expect(loadPrefs()).toEqual({ view: '3d', exaggeration: 3, tier: 'lite' });
    savePrefs({ exaggeration: 0.2 });
    expect(loadPrefs().exaggeration).toBe(1);
    expect(hasSavedTier()).toBe(true);
  });
  it('ignores junk values', () => {
    localStorage.setItem('wattson.view', 'vr');
    localStorage.setItem('wattson.view.exaggeration', 'abc');
    expect(loadPrefs()).toMatchObject({ view: '2d', exaggeration: 1.5 });
  });
  it('storage throwing gives defaults and does not throw on save', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    expect(loadPrefs()).toEqual({ view: '2d', exaggeration: 1.5, tier: 'full' });
    expect(() => savePrefs({ view: '3d' })).not.toThrow();
  });
});

describe('effectiveView', () => {
  it('no webgl: 3d unavailable', () => {
    const c = { ...ok, webgl: false };
    expect(effectiveView('3d', c)).toBe('2d');
    expect(unavailableReason('3d', c)).toBe("3D isn't supported on this display");
  });
  it('reduced motion turns 3d off', () => {
    const c = { ...ok, reducedMotion: true };
    expect(effectiveView('3d', c)).toBe('2d');
    expect(unavailableReason('3d', c)).toBe('Off because reduced motion is on');
  });
  it('synthetic route has no 3d', () => {
    expect(effectiveView('3d', { ...ok, map: false })).toBe('2d');
  });
  it('photo needs photos', () => {
    expect(effectiveView('photo', { ...ok, photos: false })).toBe('2d');
    expect(unavailableReason('photo', { ...ok, photos: false })).toBe('No street photos for this route');
    expect(effectiveView('photo', ok)).toBe('photo');
  });
  it('available views have no reason', () => {
    expect(effectiveView('3d', ok)).toBe('3d');
    expect(unavailableReason('3d', ok)).toBeNull();
    expect(unavailableReason('2d', { ...ok, webgl: false })).toBeNull();
  });
});

describe('detectTier', () => {
  it('lite on few cores or little memory', () => {
    expect(detectTier({ hardwareConcurrency: 4 })).toBe('lite');
    expect(detectTier({ hardwareConcurrency: 8, deviceMemory: 4 })).toBe('lite');
    expect(detectTier({ hardwareConcurrency: 8, deviceMemory: 8 })).toBe('full');
    expect(detectTier({})).toBe('full');
  });
});
