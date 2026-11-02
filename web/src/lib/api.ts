import createClient, { type Middleware } from 'openapi-fetch';
import type { paths } from './openapi';

const KIOSK_KEY = 'wattson.kiosk_token';

export function getKioskToken(): string | null {
  try {
    return localStorage.getItem(KIOSK_KEY);
  } catch {
    return null;
  }
}

export function setKioskToken(t: string | null): void {
  try {
    if (t) localStorage.setItem(KIOSK_KEY, t);
    else localStorage.removeItem(KIOSK_KEY);
  } catch {
    // storage unavailable (private mode); the token just does not persist
  }
}

let onUnauthorized: (returnTo: string) => void = () => {};

export function setUnauthorizedHandler(fn: (returnTo: string) => void): void {
  onUnauthorized = fn;
}

/** Runs the app's 401 handling for failures that do not come through fetch (the ride socket's 4401). */
export function notifyUnauthorized(returnTo: string): void {
  onUnauthorized(returnTo);
}

// These two endpoints answer 401 as part of normal operation.
const NO_REDIRECT = new Set(['/api/auth/login', '/api/auth/me']);
// Already on a public screen: a 401 there must not bounce the user around.
const PUBLIC_ROUTES = new Set(['/login', '/setup', '/kiosk']);

const middleware: Middleware = {
  onRequest({ request }) {
    const t = getKioskToken();
    if (t) request.headers.set('Authorization', `Bearer ${t}`);
    return request;
  },
  onResponse({ request, response }) {
    if (
      response.status === 401 &&
      !NO_REDIRECT.has(new URL(request.url).pathname) &&
      !PUBLIC_ROUTES.has(location.pathname)
    ) {
      onUnauthorized(location.pathname + location.search);
    }
    return response;
  },
};

export const api = createClient<paths>({
  baseUrl: '',
  // Resolve fetch per call so tests (and polyfills) can swap globalThis.fetch.
  fetch: (req) => globalThis.fetch(req),
});
api.use(middleware);

export class ApiError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

/** Return the response body or throw an ApiError built from {error:{code,message}}. */
export function unwrap<T>(r: { data?: T; error?: unknown; response: Response }): T {
  if (r.error !== undefined || !r.response.ok) {
    const e = (r.error as { error?: { code?: string; message?: string } } | undefined)?.error;
    throw new ApiError(
      r.response.status,
      e?.code ?? 'unknown',
      e?.message ?? `Request failed (${r.response.status})`,
    );
  }
  return r.data as T;
}
