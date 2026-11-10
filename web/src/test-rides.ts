import type { components } from '@/lib/openapi';

export type RideOut = components['schemas']['RideOut'];
export type RideDetail = components['schemas']['RideDetail'];

export const stravaNone: components['schemas']['StravaStatusOut'] = {
  status: 'none',
  activity_id: 0,
  activity_url: '',
  error: '',
  upload_id: 0,
};

export function rideFixture(over: Partial<RideOut> = {}): RideOut {
  return {
    id: 7,
    rider_id: 1,
    rider_name: 'Ann',
    route_id: 0,
    route_name: '',
    mode: 'free',
    started_at: '2026-11-02T05:05:00Z',
    ended_at: '2026-11-02T05:07:00Z',
    duration_s: 120,
    elapsed_s: 120,
    distance_m: 1200,
    ascent_m: 0,
    avg_power_w: 150,
    max_power_w: 300,
    np_w: 160,
    kj: 18,
    avg_cadence_rpm: 80,
    avg_hr_bpm: 0,
    max_hr_bpm: 0,
    avg_speed_kmh: 30,
    max_speed_kmh: 40,
    sample_count: 3,
    strava: stravaNone,
    max_cadence_rpm: 0,
    completed: false,
    ftp_w: null,
    intensity_factor: null,
    tss: null,
    variability_index: null,
    efficiency_factor: null,
    decoupling_pct: null,
    vam_m_per_h: null,
    peak_5s_w: null,
    peak_1m_w: null,
    peak_5m_w: null,
    peak_20m_w: null,
    peak_60m_w: null,
    power_zone_s: null,
    hr_zone_s: null,
    ...over,
  };
}

export function rideDetailFixture(
  over: Partial<RideDetail> = {},
  series: Partial<RideDetail['series']> = {},
): RideDetail {
  return {
    ...rideFixture(),
    series: {
      elapsed_s: [0, 1, 2],
      distance_m: [0, 600, 1200],
      power_w: [100, 200, 150],
      hr_bpm: [0, 0, 0],
      cadence_rpm: [80, 85, 82],
      speed_kmh: [25, 30, 28],
      grade_pct: [0, 0, 0],
      level: [5, 5, 5],
      ...series,
    },
    ...over,
  };
}
