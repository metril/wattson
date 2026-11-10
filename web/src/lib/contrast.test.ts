import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { contrastRatio } from './contrast';

const css = readFileSync(resolve(__dirname, '../index.css'), 'utf8');

/** A hex custom property, read from index.css so tokens and tests cannot drift. */
function token(name: string, within?: string): string {
  const scope = within ? css.slice(css.indexOf(within)) : css;
  const m = new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`).exec(scope);
  if (!m) throw new Error(`token ${name} not found`);
  return m[1];
}

const tarmac = token('--color-tarmac');
const kerb = token('--color-kerb');
const dust = token('--color-dust');
const hivis = token('--color-hivis');
// The cockpit palette block of the always-dark ride screen.
const ride = (name: string) => token(name, 'html.ride-dark {\n  --ground');
const chalk = ride('--chalk');
const raised = ride('--raised');
const line = ride('--line');

describe('contrastRatio', () => {
  it('black on white is 21', () => {
    expect(contrastRatio('#000', '#fff')).toBe(21);
    expect(contrastRatio('#fff', '#000')).toBe(21);
  });
});

describe('cockpit tokens', () => {
  it('mirror the ride-dark palette', () => {
    expect(ride('--ground').toLowerCase()).toBe(tarmac.toLowerCase());
    expect(ride('--panel').toLowerCase()).toBe(kerb.toLowerCase());
    expect(ride('--dim').toLowerCase()).toBe(dust.toLowerCase());
  });
  it('text is at least 4.5:1 on the ground and the surfaces', () => {
    for (const bg of [tarmac, kerb, raised]) {
      expect(contrastRatio(chalk, bg)).toBeGreaterThanOrEqual(4.5);
      expect(contrastRatio(dust, bg)).toBeGreaterThanOrEqual(4.5);
    }
  });
  it('focus ring and UI boundaries are at least 3:1', () => {
    expect(contrastRatio(hivis, tarmac)).toBeGreaterThanOrEqual(3);
    expect(contrastRatio(hivis, kerb)).toBeGreaterThanOrEqual(3);
    for (const bg of [tarmac, kerb]) expect(contrastRatio(line, bg)).toBeGreaterThanOrEqual(3);
  });
  it('every grade colour is at least 3:1 on the ground', () => {
    for (const g of ['flat', 'mid', 'steep', 'down']) {
      expect(contrastRatio(token(`--grade-${g}`), tarmac), g).toBeGreaterThanOrEqual(3);
    }
  });
});

describe('redesign tokens', () => {
  const themes: [string, string, string][] = [
    ['dark', ':root,\nhtml.ride-dark {', 'dark'],
    ['light', ':root:not(.ride-dark) {', 'light'],
    ['cockpit', 'html.ride-dark {\n  --ground', 'cockpit'],
  ];
  for (const [name, scope] of themes) {
    // The cockpit palette only overrides some tokens; the rest come from the dark block.
    const t = (n: string) => {
      try {
        return token(n, scope);
      } catch {
        return token(n, ':root,\nhtml.ride-dark {');
      }
    };
    it(`${name}: text and metric colours are at least 4.5:1 on ground, panel and sunken`, () => {
      for (const bg of ['--ground', '--panel', '--sunken']) {
        for (const fg of ['--chalk', '--dim', '--label', '--m-power', '--m-hr', '--m-cadence', '--m-speed', '--m-elev', '--accent']) {
          expect(contrastRatio(t(fg), t(bg)), `${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5);
        }
      }
    });
    it(`${name}: control borders are at least 3:1 on the panel`, () => {
      expect(contrastRatio(t('--line'), t('--panel'))).toBeGreaterThanOrEqual(3);
    });
  }
});
