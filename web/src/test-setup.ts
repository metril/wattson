import '@testing-library/jest-dom/vitest';

// Node's Request rejects relative URLs, but openapi-fetch is created with
// baseUrl "" (same-origin in the browser). Resolve against jsdom's location.
const NativeRequest = globalThis.Request;
class RelativeRequest extends NativeRequest {
  constructor(input: RequestInfo | URL, init?: RequestInit) {
    super(typeof input === 'string' ? new URL(input, window.location.href).href : input, init);
  }
}
globalThis.Request = RelativeRequest as typeof Request;

if (typeof globalThis.matchMedia === 'undefined') {
  globalThis.matchMedia = (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  });
}
