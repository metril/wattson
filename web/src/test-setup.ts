import '@testing-library/jest-dom/vitest';
import { configure } from '@testing-library/react';

// De-flake findBy*/waitFor when the machine is loaded by parallel workers.
configure({ asyncUtilTimeout: 3000 });

// Node's Request rejects relative URLs, but openapi-fetch is created with
// baseUrl "" (same-origin in the browser). Resolve against jsdom's location.
const NativeRequest = globalThis.Request;
// jsdom's AbortSignal is not the one undici's Request accepts, so the shim drops it.
// Pages that abort check their own signal after the await, which is what tests exercise.
function nativeInit(init?: RequestInit): RequestInit | undefined {
  return init?.signal ? { ...init, signal: null } : init;
}
class RelativeRequest extends NativeRequest {
  constructor(input: RequestInfo | URL, init?: RequestInit) {
    super(typeof input === 'string' ? new URL(input, window.location.href).href : input, nativeInit(init));
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

// jsdom has no canvas; draw code skips a null context instead of logging "not implemented".
HTMLCanvasElement.prototype.getContext = (() => null) as typeof HTMLCanvasElement.prototype.getContext;

if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
}

// ECharts needs a real canvas; chart components are tested through their option builders.
vi.mock('@/features/charts/EChart', () => ({ default: () => null }));
