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
  grade_pct: number;
  level: number;
  reported_level: number;
  connected: boolean;
  alert?: string;
};

export type BikeStatus = components['schemas']['Status'];

export type ServerMsg =
  | { type: 'state'; bike: BikeStatus; tick: Tick }
  | { type: 'tick'; tick: Tick }
  | { type: 'bike'; bike: BikeStatus }
  | { type: 'ack'; id: string }
  | { type: 'error'; id?: string; error: { code: string; message: string } };

/** set_target_power exists in the protocol but is not supported yet. */
export const COMMAND_NAMES = ['start', 'pause', 'resume', 'stop', 'set_level', 'level_delta'] as const;
export type CommandName = (typeof COMMAND_NAMES)[number];

export const SERVER_MSG_TYPES = ['state', 'tick', 'bike', 'ack', 'error'] as const satisfies readonly ServerMsg['type'][];
