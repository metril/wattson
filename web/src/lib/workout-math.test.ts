import { blockErrors, defaultBlock, mmss, parseDuration, resolve, totalS, type RiderLite } from './workout-math';

const rider: RiderLite = { offset_level: 5, min_level: 2, max_level: 16, ftp_w: 200 };

describe('workout-math', () => {
  it('resolves interval blocks like Go TestResolve', () => {
    const out = resolve(
      'interval',
      [
        { duration_s: 60, level_offset: -10 },
        { duration_s: 90, level_offset: 3 },
        { duration_s: 120, level: 20 },
        { duration_s: 30, level: 1 },
      ],
      rider,
    );
    expect(out.map((o) => o.value)).toEqual([2, 8, 16, 2]);
    expect(out.map((o) => o.startS)).toEqual([0, 60, 150, 270]);
    expect(out.map((o) => o.durS)).toEqual([60, 90, 120, 30]);
  });

  it('never resolves level 0, even with min level 0', () => {
    const [b] = resolve('interval', [{ duration_s: 10, level_offset: -31 }], { ...rider, min_level: 0 });
    expect(b.value).toBe(1);
  });

  it('resolves ERG watts and clamps to 50..1000', () => {
    expect(resolve('erg', [{ duration_s: 60, ftp_pct: 75 }, { duration_s: 45, power_w: 230 }], rider).map((o) => o.value)).toEqual([
      150, 230,
    ]);
    expect(resolve('erg', [{ duration_s: 60, ftp_pct: 100 }], { ...rider, ftp_w: 0 })[0].value).toBe(50);
    expect(resolve('erg', [{ duration_s: 60, ftp_pct: 200 }], { ...rider, ftp_w: 900 })[0].value).toBe(1000);
  });

  it('totals durations', () => {
    expect(totalS([{ duration_s: 60 }, { duration_s: 120 }])).toBe(180);
  });

  it('parses durations', () => {
    expect(parseDuration('90')).toBe(90);
    expect(parseDuration('1:30')).toBe(90);
    expect(parseDuration('1:00:00')).toBe(3600);
    expect(parseDuration(' 2:00 ')).toBe(120);
    for (const bad of ['x', '', '1:60', '1:2:3:4', '-5', '1.5', '1::2']) expect(parseDuration(bad)).toBeNull();
  });

  it('formats durations', () => {
    expect(mmss(90)).toBe('1:30');
    expect(mmss(5)).toBe('0:05');
    expect(mmss(3600)).toBe('1:00:00');
    expect(mmss(3725)).toBe('1:02:05');
  });

  it('builds default blocks', () => {
    expect(defaultBlock('interval')).toEqual({ duration_s: 60, level_offset: 0 });
    expect(defaultBlock('erg')).toEqual({ duration_s: 60, ftp_pct: 60 });
  });

  describe('blockErrors', () => {
    it('accepts valid workouts', () => {
      expect(blockErrors('interval', [{ duration_s: 60, level: 5 }, defaultBlock('interval')])).toEqual({});
      expect(blockErrors('erg', [{ duration_s: 60, power_w: 200 }, defaultBlock('erg')])).toEqual({});
    });
    it('flags a field of the wrong type', () => {
      const e = blockErrors('interval', [{ duration_s: 60, power_w: 200 }]);
      expect(e).toHaveProperty(['blocks[0].power_w']);
      expect(e).toHaveProperty(['blocks[0].level']); // and the missing target
    });
    it('flags ERG without a target and out-of-range values', () => {
      expect(blockErrors('erg', [{ duration_s: 60 }])).toHaveProperty(['blocks[0].power_w']);
      expect(blockErrors('erg', [{ duration_s: 60, power_w: 20 }])).toHaveProperty(['blocks[0].power_w']);
      expect(blockErrors('erg', [{ duration_s: 60, ftp_pct: 250 }])).toHaveProperty(['blocks[0].ftp_pct']);
      expect(blockErrors('interval', [{ duration_s: 60, level: 33 }])).toHaveProperty(['blocks[0].level']);
      expect(blockErrors('interval', [{ duration_s: 60, level_offset: 40 }])).toHaveProperty(['blocks[0].level_offset']);
    });
    it('rejects both targets at once', () => {
      expect(blockErrors('interval', [{ duration_s: 60, level: 3, level_offset: 1 }])).toHaveProperty(['blocks[0].level']);
    });
    it('flags duration, count and total', () => {
      expect(blockErrors('interval', [{ duration_s: 4, level: 3 }])).toHaveProperty(['blocks[0].duration_s']);
      expect(blockErrors('interval', [{ duration_s: 7201, level: 3 }])).toHaveProperty(['blocks[0].duration_s']);
      expect(blockErrors('interval', [])).toHaveProperty('blocks');
      const many = Array.from({ length: 4 }, () => ({ duration_s: 7200, level: 3 }));
      expect(blockErrors('interval', many)).toHaveProperty('blocks');
    });
    it('flags long cadence and label', () => {
      const e = blockErrors('interval', [{ duration_s: 60, level: 3, cadence: 'x'.repeat(33), label: 'y'.repeat(41) }]);
      expect(e).toHaveProperty(['blocks[0].cadence']);
      expect(e).toHaveProperty(['blocks[0].label']);
    });
  });
});
