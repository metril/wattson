import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { COMMAND_NAMES, SERVER_MSG_TYPES, type BikeStatus, type RideState, type ServerMsg, type Tick } from './ws-types';

const spec = JSON.parse(
  readFileSync(resolve(process.cwd(), '../docs/asyncapi.yaml'), 'utf8'),
);
const schemas = spec.components.schemas;

// Required<> makes the compiler reject a sample that misses a typed field.
const sampleTick: Required<Tick> = {
  at: '',
  state: 'idle',
  mode: '',
  elapsed_s: 0,
  distance_m: 0,
  speed_kmh: 0,
  power_w: 0,
  cadence_rpm: 0,
  grade_pct: 0,
  level: 0,
  reported_level: 0,
  connected: false,
  alert: '',
};
const sampleStatus: Required<BikeStatus> = {
  connected: false,
  driver: '',
  floor_unconfirmed: false,
  hint: '',
  last_error: '',
  protocol: '',
  rider_id: 0,
  since: '',
  state: '',
};

const keys = (o: object) => Object.keys(o).sort();

describe('ws-types vs docs/asyncapi.yaml', () => {
  it('Tick fields match', () => {
    expect(keys(schemas.Tick.properties)).toEqual(keys(sampleTick));
  });
  it('Status fields match', () => {
    expect(keys(schemas.Status.properties)).toEqual(keys(sampleStatus));
  });
  it('command names match, minus set_target_power', () => {
    const names = (schemas.WSCommand.properties.command.enum as string[]).filter((c) => c !== 'set_target_power');
    expect([...names].sort()).toEqual([...COMMAND_NAMES].sort());
  });
  it('Tick.state values match RideState', () => {
    const states: Record<RideState, true> = { idle: true, connecting: true, ready: true, riding: true, paused: true, finished: true };
    expect([...schemas.Tick.properties.state.enum].sort()).toEqual(Object.keys(states).sort());
  });
  it('WSError shape matches ServerMsg', () => {
    const err = schemas.WSError;
    expect(keys(err.properties)).toEqual(['error', 'id', 'type']);
    expect([...err.required].sort()).toEqual(['error', 'type']); // id is optional
    expect(keys(schemas.ErrorBody.properties)).toEqual(['code', 'message']);
    const sample: Extract<ServerMsg, { type: 'error' }> = { type: 'error', id: '', error: { code: '', message: '' } };
    expect(keys(sample)).toEqual(keys(err.properties));
    expect(keys(sample.error)).toEqual(keys(schemas.ErrorBody.properties));
  });
  it('server message types match', () => {
    const types = ['WSState', 'WSTick', 'WSBike', 'WSAck', 'WSError'].map((n) => schemas[n].properties.type.enum[0]);
    expect([...types].sort()).toEqual([...SERVER_MSG_TYPES].sort());
  });
});
