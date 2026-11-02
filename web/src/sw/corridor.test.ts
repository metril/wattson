import {
  CACHE,
  cacheKey,
  corridorTiles,
  DEM_ZOOMS,
  evictCount,
  isTileRequest,
  lonLatToTile,
  tileUrls,
  vectorTemplate,
  VECTOR_ZOOMS,
  routeUrls,
} from './corridor';

const DEM = 'https://s3.amazonaws.com/elevation-tiles-prod/terrarium/12/2133/1465.png';

// The standard slippy-map formula, written independently of the implementation.
function slippy(lon: number, lat: number, z: number): [number, number] {
  const n = 2 ** z;
  const r = (lat * Math.PI) / 180;
  return [Math.floor(((lon + 180) / 360) * n), Math.floor(((1 - Math.log(Math.tan(r) + 1 / Math.cos(r)) / Math.PI) / 2) * n)];
}

describe('lonLatToTile', () => {
  it('matches the slippy formula', () => {
    expect(lonLatToTile(7.5, 45.5, 14)).toEqual([8533, 5861]);
    expect(lonLatToTile(7.5, 45.5, 14)).toEqual(slippy(7.5, 45.5, 14));
    expect(lonLatToTile(-122.4, 37.8, 10)).toEqual(slippy(-122.4, 37.8, 10));
  });
  it('clamps poles and the date line into the grid', () => {
    expect(lonLatToTile(180, 89, 3)).toEqual([7, 0]);
    expect(lonLatToTile(-180, -89, 3)).toEqual([0, 7]);
  });
});

describe('corridorTiles', () => {
  // ~1 km east at 45.5 N
  const lon = [7.5, 7.5 + 0.0128];
  const lat = [45.5, 45.5];

  it('covers a short line at z14 with a handful of unique tiles including the start', () => {
    const t = corridorTiles(lon, lat, { 14: 300 });
    expect(t.length).toBeGreaterThanOrEqual(1);
    expect(t.length).toBeLessThanOrEqual(4);
    expect(new Set(t).size).toBe(t.length);
    expect(t).toContain('14/8533/5861');
  });

  it('a 20 km buffer at z8 stays within a 3x3 block', () => {
    expect(corridorTiles(lon, lat, { 8: 20000 }).length).toBeLessThanOrEqual(9);
  });

  it('orders tiles by distance along the route', () => {
    const t = corridorTiles([7.5, 7.7], [45.5, 45.5], { 14: 300 });
    const xs = t.map((s) => Number(s.split('/')[1]));
    expect(xs[0]).toBe(8533);
    expect(xs[xs.length - 1]).toBeGreaterThan(xs[0]);
    expect(xs.slice().sort((a, b) => a - b)[0]).toBe(xs[0]);
  });

  it('handles empty and single-point routes', () => {
    expect(corridorTiles([], [], VECTOR_ZOOMS)).toEqual([]);
    expect(corridorTiles([7.5], [45.5], { 14: 300 })).toContain('14/8533/5861');
  });

  it('includes every requested zoom', () => {
    const t = corridorTiles(lon, lat, DEM_ZOOMS);
    for (const z of Object.keys(DEM_ZOOMS)) expect(t.some((s) => s.startsWith(`${z}/`))).toBe(true);
  });
});

describe('tileUrls and routeUrls', () => {
  it('fills the template', () => {
    expect(tileUrls(['14/1/2'], 'https://x/{z}/{x}/{y}.pbf')).toEqual(['https://x/14/1/2.pbf']);
  });
  it('interleaves vector and DEM urls and caps the list', () => {
    const urls = routeUrls({ lon: [7.5, 7.6, 7.7], lat: [45.5, 45.5, 45.5] }, 'https://tiles.openfreemap.org/planet/v/{z}/{x}/{y}.pbf', 5);
    expect(urls).toHaveLength(5);
    expect(urls.some((u) => u.includes('openfreemap'))).toBe(true);
    expect(urls.some((u) => u.includes('terrarium'))).toBe(true);
    expect(urls.every(isTileRequest)).toBe(true);
  });
});

describe('isTileRequest', () => {
  it('accepts map tiles', () => {
    expect(isTileRequest('https://tiles.openfreemap.org/planet/20250101_001001_pt/14/1/2.pbf')).toBe(true);
    expect(isTileRequest(DEM)).toBe(true);
  });
  it('rejects styles, the app, other buckets and the unversioned TileJSON', () => {
    expect(isTileRequest('https://tiles.openfreemap.org/styles/liberty')).toBe(false);
    expect(isTileRequest('https://tiles.openfreemap.org/planet')).toBe(false);
    expect(isTileRequest('https://example.com/api/routes')).toBe(false);
    expect(isTileRequest('/api/routes')).toBe(false);
    expect(isTileRequest('https://s3.amazonaws.com/other-bucket/terrarium/12/1/2.png')).toBe(false);
    expect(isTileRequest('https://s3.amazonaws.com/elevation-tiles-prod/skadi/N45/N45E007.hgt.gz')).toBe(false);
    expect(isTileRequest('http://tiles.openfreemap.org/planet/v/14/1/2.pbf')).toBe(false);
  });
});

describe('cacheKey and evictCount', () => {
  it('drops query and hash', () => {
    expect(cacheKey('https://tiles.openfreemap.org/planet/v/14/1/2.pbf?v=1#x')).toBe('https://tiles.openfreemap.org/planet/v/14/1/2.pbf');
  });
  it('evicts in steps of 10 percent of max', () => {
    expect(evictCount(3990, 20)).toBe(400);
    expect(evictCount(10, 5)).toBe(0);
    expect(evictCount(4000, 0)).toBe(0);
    expect(evictCount(4000, 1)).toBe(400);
    expect(evictCount(4000, 900)).toBe(1200);
    expect(evictCount(90, 20, 100)).toBe(10);
  });
  it('names a versioned cache', () => {
    expect(CACHE).toBe('wattson-tiles-v1');
  });
});

describe('vectorTemplate', () => {
  it('reads tiles[0] from the TileJSON', async () => {
    const f = vi.fn(async () => new Response(JSON.stringify({ tiles: ['https://tiles.openfreemap.org/planet/v1/{z}/{x}/{y}.pbf'] })));
    await expect(vectorTemplate(f as unknown as typeof fetch)).resolves.toBe('https://tiles.openfreemap.org/planet/v1/{z}/{x}/{y}.pbf');
    expect(f).toHaveBeenCalledWith('https://tiles.openfreemap.org/planet');
  });
  it('rejects on a bad answer', async () => {
    await expect(vectorTemplate((async () => new Response('{}', { status: 200 })) as unknown as typeof fetch)).rejects.toThrow();
    await expect(vectorTemplate((async () => new Response('x', { status: 500 })) as unknown as typeof fetch)).rejects.toThrow();
  });
});
