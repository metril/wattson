import { HR_EDGES, POWER_EDGES, pctLabel, rangeLabel, zoneBounds, zoneRanges } from './zones';

describe('zones', () => {
  it('matches the server cut-offs (internal/stats PowerBounds and HRBounds)', () => {
    expect(POWER_EDGES).toEqual([0.55, 0.75, 0.9, 1.05, 1.2, 1.5]);
    expect(HR_EDGES).toEqual([0.6, 0.7, 0.8, 0.9]);
    expect(zoneBounds('power', 200)).toEqual([110, 150, 180, 210, 240, 300].map((n) => expect.closeTo(n, 9)));
    expect(zoneBounds('hr', 190)).toEqual([114, 133, 152, 171].map((n) => expect.closeTo(n, 9)));
  });

  it('builds contiguous inclusive ranges', () => {
    const r = zoneRanges('power', 200);
    expect(r).toHaveLength(7);
    expect(rangeLabel(r[0], 'W')).toBe('<110 W');
    expect(rangeLabel(r[1], 'W')).toBe('110–149 W');
    expect(rangeLabel(r[3], 'W')).toBe('180–209 W');
    expect(rangeLabel(r[6], 'W')).toBe('300+ W');
    for (let i = 1; i < r.length; i++) expect(r[i].lo).toBe(r[i - 1].hi! + 1);
    expect(pctLabel(r[1], 'FTP')).toBe('56–75% FTP');
    expect(pctLabel(r[0], 'FTP')).toBe('<55% FTP');
    expect(pctLabel(r[6], 'FTP')).toBe('>150% FTP');
  });

  it('has five heart-rate zones and no ranges without a base', () => {
    const r = zoneRanges('hr', null);
    expect(r).toHaveLength(5);
    expect(rangeLabel(r[2], 'bpm')).toBe('');
    expect(rangeLabel(zoneRanges('hr', 190)[4], 'bpm')).toBe('171+ bpm');
  });
});
