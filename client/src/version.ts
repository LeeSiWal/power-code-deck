// APP_VERSION is the release number, injected at build time by vite.config.ts from
// server/version/VERSION — the SAME file the Go server embeds. Import this instead of
// hardcoding a version string anywhere in the UI, so there is one place to bump.
//
// At runtime the true value still comes from the server's /api/health (authConfig.
// version); APP_VERSION is the build-time fallback for when that hasn't loaded yet.
export const APP_VERSION: string = __APP_VERSION__;
