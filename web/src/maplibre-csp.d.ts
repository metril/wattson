// The CSP build has the same API as the default one but ships its worker as a separate file (see lib/maplibre.ts).
declare module 'maplibre-gl/dist/maplibre-gl-csp' {
  export * from 'maplibre-gl';
}
