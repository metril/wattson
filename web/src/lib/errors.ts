import { ApiError } from './api';

/** One-line, user-facing text for a failed request. */
export function errMessage(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.code === 'bike_busy') return 'Release the bike on the Ride screen first.';
    return e.message;
  }
  return "Couldn't reach the server.";
}

/** `section.field: problem` as returned with 400 invalid_setting. */
export function parseSettingError(e: unknown): { field?: string; text: string } {
  if (e instanceof ApiError && e.code === 'invalid_setting') {
    const m = /^([a-z0-9_]+\.[a-z0-9_]+):\s*(.*)$/s.exec(e.message);
    if (m) return { field: m[1], text: m[2] };
  }
  return { text: errMessage(e) };
}
