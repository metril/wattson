/** BRouter 5 degree tile name (E5_N45) for a point; mirrors route.TileName. Out-of-range input is clamped. */
export function tileName(lat: number, lon: number): string {
  if (Number.isNaN(lat) || Number.isNaN(lon)) return '';
  return nameOf(
    Math.max(-90, Math.min(85, Math.floor(lat / 5) * 5)),
    Math.max(-180, Math.min(175, Math.floor(lon / 5) * 5)),
  );
}

function nameOf(lat0: number, lon0: number): string {
  return `${lon0 < 0 ? 'W' : 'E'}${Math.abs(lon0)}_${lat0 < 0 ? 'S' : 'N'}${Math.abs(lat0)}`;
}

/** Every tile of the world: 72 x 36, each with its [west, south, east, north] box. */
export function tileGrid(): { name: string; bbox: [number, number, number, number] }[] {
  const out: { name: string; bbox: [number, number, number, number] }[] = [];
  for (let lat = -90; lat < 90; lat += 5) {
    for (let lon = -180; lon < 180; lon += 5) {
      out.push({ name: nameOf(lat, lon), bbox: [lon, lat, lon + 5, lat + 5] });
    }
  }
  return out;
}
