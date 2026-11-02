import type { components } from './openapi';

/** Mirrors engine.Tick; checked against docs/asyncapi.yaml by ws-types.test.ts. */
export type RideState = 'idle' | 'connecting' | 'ready' | 'riding' | 'paused' | 'finished';

export type Tick = {
  at: string;
  state: RideState;
  mode: string;
  elapsed_s: number;
  distance_m: number;
  speed_kmh: number;
  power_w: number;
  cadence_rpm: number;
  heart_rate_bpm: number;
  grade_pct: number;
  level: number;
  reported_level: number;
  connected: boolean;
  alert?: string;
  route_id: number;
  route_length_m: number;
  grade_ahead_pct: number;
  bias: number;
  workout_id: number;
  block_index: number;
  block_count: number;
  block_remaining_s: number;
  target_power_w: number;
};

export type BikeStatus = components['schemas']['Status'];

export type ServerMsg =
  | { type: 'state'; bike: BikeStatus; tick: Tick }
  | { type: 'tick'; tick: Tick }
  | { type: 'bike'; bike: BikeStatus }
  | { type: 'ack'; id: string }
  | { type: 'error'; id?: string; error: { code: string; message: string; details?: { missing_tiles?: string[] } } };

export const COMMAND_NAMES = ['start', 'pause', 'resume', 'stop', 'set_level', 'level_delta', 'set_target_power'] as const;
export type CommandName = (typeof COMMAND_NAMES)[number];

/** The one message a client sends; route_id, workout_id and target_power_w only accompany start, and at most one of them. */
export type CommandMsg = {
  type: 'command';
  id: string;
  command: CommandName;
  value: number;
  route_id?: number;
  workout_id?: number;
  target_power_w?: number;
};

export const SERVER_MSG_TYPES = ['state', 'tick', 'bike', 'ack', 'error'] as const satisfies readonly ServerMsg['type'][];
