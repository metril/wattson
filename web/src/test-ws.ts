import type { BikeStatus, Tick } from '@/lib/ws-types';

/** Minimal WebSocket stand-in; tests drive it with open/receive/drop. */
export class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  static reset() {
    FakeWebSocket.instances = [];
  }
  static get last(): FakeWebSocket {
    return FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  }

  readyState = 0;
  sent: string[] = [];
  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;

  constructor(
    public url: string,
    public protocols?: string | string[],
  ) {
    FakeWebSocket.instances.push(this);
  }

  send(d: string) {
    this.sent.push(d);
  }
  close() {
    this.readyState = 3;
  }

  open() {
    this.readyState = 1;
    this.onopen?.({});
  }
  receive(msg: unknown) {
    this.onmessage?.({ data: JSON.stringify(msg) });
  }
  drop(code = 1006) {
    this.readyState = 3;
    this.onclose?.({ code });
  }
  /** Parsed commands the client has sent. */
  get commands(): { id: string; command: string; value: number }[] {
    return this.sent.map((s) => JSON.parse(s));
  }
}

export const tickFixture = (over: Partial<Tick> = {}): Tick => ({
  at: '2026-01-01T00:00:00Z',
  state: 'ready',
  mode: 'free',
  elapsed_s: 0,
  distance_m: 0,
  speed_kmh: 0,
  power_w: 0,
  cadence_rpm: 0,
  grade_pct: 0,
  level: 5,
  reported_level: 0,
  connected: true,
  ...over,
});

export const bikeFixture = (over: Partial<BikeStatus> = {}): BikeStatus => ({
  state: 'ready',
  connected: true,
  floor_unconfirmed: false,
  since: '2026-01-01T00:00:00Z',
  rider_id: 1,
  protocol: 'sim',
  ...over,
});
