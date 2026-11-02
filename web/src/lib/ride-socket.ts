import { ApiError, getKioskToken } from './api';
import { TickStore } from './tick-store';
import type { BikeStatus, CommandMsg, CommandName, RideState, ServerMsg } from './ws-types';

export interface RideStatus {
  connected: boolean;
  bike: BikeStatus | null;
  state: RideState;
  /** Latest tick alert; undefined when none. */
  alert?: string;
}

interface Opts {
  url?: string;
  token?: string | null;
  onUnauthorized(): void;
  WebSocketImpl?: typeof WebSocket;
}

export type StartOpts = { route_id?: number; workout_id?: number; target_power_w?: number };

const BACKOFF_S = [1, 2, 4, 8, 10];
const COMMAND_TIMEOUT_MS = 5000;
const OPEN = 1;

function defaultUrl(): string {
  return `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws`;
}

interface Pending {
  resolve(): void;
  reject(e: ApiError): void;
  timer: ReturnType<typeof setTimeout>;
}

/**
 * The ride WebSocket: reconnects with backoff, turns commands into promises
 * settled by ack/error, and keeps ticks in a TickStore rather than in React.
 */
export class RideSocket {
  readonly ticks = new TickStore();
  private opts: Opts;
  private ws: WebSocket | null = null;
  private closed = true;
  private attempt = 0;
  private retry: ReturnType<typeof setTimeout> | undefined;
  private seq = 0;
  private pending = new Map<string, Pending>();
  private listeners = new Set<(s: RideStatus) => void>();
  private status: RideStatus = { connected: false, bike: null, state: 'idle' };
  private bikeKey = '';
  /** Consecutive connections that closed without ever opening. */
  private failStreak = 0;
  private probing = false;

  constructor(opts: Opts) {
    this.opts = opts;
  }

  start(): void {
    this.closed = false;
    if (!this.ws) this.connect();
  }

  close(): void {
    this.closed = true;
    clearTimeout(this.retry);
    const ws = this.ws;
    this.ws = null;
    if (ws) {
      ws.onopen = ws.onmessage = ws.onclose = ws.onerror = null;
      ws.close();
    }
    this.failPending('disconnected', 'The connection to the bike closed.');
    this.update({ connected: false });
  }

  getStatus(): RideStatus {
    return this.status;
  }

  /** Subscribes to status changes (connected, bike, state, alert); never fires for plain ticks. */
  onStatus(fn: (s: RideStatus) => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  /** `opts` carries what start should ride (a route, a workout or a free-ERG target); only the given keys are sent. */
  send(command: CommandName, value = 0, opts?: StartOpts): Promise<void> {
    const ws = this.ws;
    if (!ws || ws.readyState !== OPEN) {
      return Promise.reject(new ApiError(0, 'disconnected', 'Not connected to the server.'));
    }
    const id = `c${++this.seq}`;
    return new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new ApiError(0, 'timeout', 'The server did not answer in time.'));
      }, COMMAND_TIMEOUT_MS);
      this.pending.set(id, { resolve, reject, timer });
      const msg: CommandMsg = { type: 'command', id, command, value };
      if (opts?.route_id !== undefined) msg.route_id = opts.route_id;
      if (opts?.workout_id !== undefined) msg.workout_id = opts.workout_id;
      if (opts?.target_power_w !== undefined) msg.target_power_w = opts.target_power_w;
      ws.send(JSON.stringify(msg));
    });
  }

  private connect(): void {
    const { url = defaultUrl(), token, WebSocketImpl } = this.opts;
    const Impl = WebSocketImpl ?? globalThis.WebSocket;
    const protocols = token ? ['wattson.v1', `bearer.${token}`] : ['wattson.v1'];
    const ws = new Impl(url, protocols);
    this.ws = ws;
    let opened = false;
    ws.onopen = () => {
      opened = true;
      this.failStreak = 0;
    };
    ws.onmessage = (ev) => this.onMessage(ev.data);
    ws.onclose = (ev) => {
      if (this.ws !== ws) return;
      this.ws = null;
      if (!opened) this.failStreak++;
      this.failPending('disconnected', 'The connection to the bike closed.');
      this.update({ connected: false });
      if (ev.code === 4401) {
        this.closed = true;
        this.opts.onUnauthorized();
        return;
      }
      if (this.closed) return;
      if (this.failStreak >= 2) void this.probeSession();
      const delay = BACKOFF_S[Math.min(this.attempt, BACKOFF_S.length - 1)] * 1000;
      this.attempt++;
      this.retry = setTimeout(() => {
        if (!this.closed) this.connect();
      }, delay);
    };
  }

  /**
   * A browser never sees the HTTP status of a failed WebSocket handshake, so
   * after repeated failures to even open, ask the API whether the credential
   * is still good. A 401 means reconnecting is pointless: sign in again.
   */
  private async probeSession(): Promise<void> {
    if (this.probing) return;
    this.probing = true;
    try {
      const { token } = this.opts;
      const res = await globalThis.fetch('/api/auth/me', {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
        credentials: 'same-origin',
      });
      if (res.status === 401 && !this.closed) {
        this.closed = true;
        clearTimeout(this.retry);
        this.opts.onUnauthorized();
      }
    } catch {
      // server unreachable: keep retrying
    } finally {
      this.probing = false;
    }
  }

  private onMessage(data: unknown): void {
    let m: ServerMsg;
    try {
      m = JSON.parse(String(data)) as ServerMsg;
    } catch {
      return;
    }
    switch (m.type) {
      case 'state':
        this.attempt = 0;
        this.ticks.set(m.tick);
        this.update({ connected: true, bike: m.bike, tick: m.tick });
        break;
      case 'tick':
        this.ticks.set(m.tick);
        this.update({ tick: m.tick });
        break;
      case 'bike':
        this.update({ bike: m.bike });
        break;
      case 'ack':
        this.settle(m.id)?.resolve();
        break;
      case 'error':
        if (m.id) this.settle(m.id)?.reject(new ApiError(0, m.error.code, m.error.message));
        break;
    }
  }

  private settle(id: string): Pending | undefined {
    const p = this.pending.get(id);
    if (p) {
      clearTimeout(p.timer);
      this.pending.delete(id);
    }
    return p;
  }

  private failPending(code: string, message: string): void {
    for (const id of [...this.pending.keys()]) this.settle(id)?.reject(new ApiError(0, code, message));
  }

  /** Merge a change; notify only when connected, bike, state or alert differ. */
  private update(p: { connected?: boolean; bike?: BikeStatus; tick?: { state: RideState; alert?: string } }): void {
    const prev = this.status;
    let bike = prev.bike;
    if (p.bike) {
      const key = JSON.stringify(p.bike);
      if (key !== this.bikeKey) {
        this.bikeKey = key;
        bike = p.bike;
      }
    }
    const tickState = p.tick?.state ?? this.ticks.latest?.state;
    // Before a bike is live the engine tick still says idle, so the bike's own state wins then.
    const bikeState = bike?.state as RideState | undefined;
    const state: RideState = bikeState === 'idle' || bikeState === 'connecting' ? bikeState : (tickState ?? bikeState ?? 'idle');
    const alert = p.tick ? p.tick.alert || undefined : prev.alert;
    const connected = p.connected ?? prev.connected;
    if (connected === prev.connected && bike === prev.bike && state === prev.state && alert === prev.alert) return;
    this.status = { connected, bike, state, alert };
    for (const fn of this.listeners) fn(this.status);
  }
}

/** A socket wired to the app's kiosk token (if paired). */
export function createRideSocket(onUnauthorized: () => void): RideSocket {
  return new RideSocket({ token: getKioskToken(), onUnauthorized });
}
