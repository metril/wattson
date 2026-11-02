import type { components } from './openapi';

export type Block = components['schemas']['BlockIO'];
export type WorkoutType = 'interval' | 'erg';
export type RiderLite = { offset_level: number; min_level: number; max_level: number; ftp_w: number };

// Mirror internal/workout and internal/engine limits.
export const MAX_BLOCKS = 200;
export const MIN_BLOCK_S = 5;
export const MAX_BLOCK_S = 7200;
export const MAX_TOTAL_S = 21600;
export const MIN_TARGET_W = 50;
export const MAX_TARGET_W = 1000;

export function totalS(bs: Block[]): number {
  return bs.reduce((n, b) => n + b.duration_s, 0);
}

/** Each block's start, length and resolved level (interval) or watts (ERG); the same rule as Go workout.Resolve. */
export function resolve(
  t: WorkoutType,
  bs: Block[],
  r: RiderLite,
): { startS: number; durS: number; value: number }[] {
  const lo = Math.max(1, r.min_level);
  const hi = Math.max(lo, r.max_level);
  let startS = 0;
  return bs.map((b) => {
    let value = 0;
    if (t === 'interval') {
      if (b.level !== undefined) value = b.level;
      else if (b.level_offset !== undefined) value = Math.round(r.offset_level) + b.level_offset;
      value = Math.min(Math.max(value, lo), hi);
    } else {
      if (b.power_w !== undefined) value = b.power_w;
      else if (b.ftp_pct !== undefined) value = Math.round((b.ftp_pct * r.ftp_w) / 100);
      value = Math.max(MIN_TARGET_W, Math.min(MAX_TARGET_W, value));
    }
    const out = { startS, durS: b.duration_s, value };
    startS += b.duration_s;
    return out;
  });
}

const isInt = (n: number | undefined): n is number => typeof n === 'number' && Number.isInteger(n);

function range(n: number | undefined, lo: number, hi: number): boolean {
  return isInt(n) && n >= lo && n <= hi;
}

/** Field errors keyed "blocks[i].field" (or "blocks"); the same rules as Go workout.Validate, but every error is reported. */
export function blockErrors(t: WorkoutType, bs: Block[]): Record<string, string> {
  const e: Record<string, string> = {};
  if (bs.length < 1 || bs.length > MAX_BLOCKS) e.blocks = `Use 1 to ${MAX_BLOCKS} blocks.`;
  bs.forEach((b, i) => {
    const k = (f: string) => `blocks[${i}].${f}`;
    if (!range(b.duration_s, MIN_BLOCK_S, MAX_BLOCK_S)) {
      e[k('duration_s')] = `Duration must be ${mmss(MIN_BLOCK_S)} to ${mmss(MAX_BLOCK_S)}.`;
    }
    if (t === 'interval') {
      if (b.power_w !== undefined) e[k('power_w')] = 'Watts are not allowed in an interval workout.';
      if (b.ftp_pct !== undefined) e[k('ftp_pct')] = '% FTP is not allowed in an interval workout.';
      if ((b.level === undefined) === (b.level_offset === undefined)) {
        e[k('level')] = 'Set a level or an offset.';
      } else if (b.level !== undefined && !range(b.level, 1, 32)) {
        e[k('level')] = 'Level must be 1 to 32.';
      } else if (b.level_offset !== undefined && !range(b.level_offset, -31, 31)) {
        e[k('level_offset')] = 'Offset must be -31 to 31.';
      }
    } else {
      if (b.level !== undefined) e[k('level')] = 'Level is not allowed in an ERG workout.';
      if (b.level_offset !== undefined) e[k('level_offset')] = 'Offset is not allowed in an ERG workout.';
      if ((b.power_w === undefined) === (b.ftp_pct === undefined)) {
        e[k('power_w')] = 'Set watts or % FTP.';
      } else if (b.power_w !== undefined && !range(b.power_w, MIN_TARGET_W, MAX_TARGET_W)) {
        e[k('power_w')] = `Watts must be ${MIN_TARGET_W} to ${MAX_TARGET_W}.`;
      } else if (b.ftp_pct !== undefined && !range(b.ftp_pct, 20, 200)) {
        e[k('ftp_pct')] = '% FTP must be 20 to 200.';
      }
    }
    if ([...(b.cadence ?? '')].length > 32) e[k('cadence')] = 'At most 32 characters.';
    if ([...(b.label ?? '')].length > 40) e[k('label')] = 'At most 40 characters.';
  });
  if (!e.blocks && totalS(bs) > MAX_TOTAL_S) e.blocks = `Total must not exceed ${mmss(MAX_TOTAL_S)}.`;
  return e;
}

/** "90", "1:30" or "1:00:00" to seconds; null when it is not one of those. */
export function parseDuration(s: string): number | null {
  const parts = s.trim().split(':');
  if (parts.length > 3 || parts.some((p) => !/^\d{1,5}$/.test(p))) return null;
  const nums = parts.map(Number);
  if (nums.length > 1 && nums.slice(1).some((n) => n > 59)) return null;
  return nums.reduce((acc, n) => acc * 60 + n, 0);
}

/** 90 becomes "1:30", 3600 becomes "1:00:00". */
export function mmss(s: number): string {
  const n = Math.max(0, Math.round(s));
  const h = Math.floor(n / 3600);
  const m = Math.floor((n % 3600) / 60);
  const sec = String(n % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
}

export function defaultBlock(t: WorkoutType): Block {
  return t === 'interval' ? { duration_s: 60, level_offset: 0 } : { duration_s: 60, ftp_pct: 60 };
}
