/// <reference types="vite/client" />

// Injected by vite.config.ts from server/version/VERSION — the single source of truth
// for the release number, shared with the Go server.
declare const __APP_VERSION__: string;

declare module '*.wasm?url' {
  const src: string;
  export default src;
}
