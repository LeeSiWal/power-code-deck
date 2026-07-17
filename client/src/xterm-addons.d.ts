/**
 * Minimal typing for the unicode11 addon's runtime entry.
 *
 * The package's own typings `import { Terminal } from '@xterm/xterm'` — the DOM
 * build, which we don't install (we parse with @xterm/headless and render the
 * grid ourselves). Importing the shipped .d.ts would therefore fail to resolve,
 * so we declare the one class we use and type its terminal argument structurally:
 * activate() only ever calls `terminal.unicode.register(...)`, which headless has.
 */
declare module '@xterm/addon-unicode11/lib/addon-unicode11.js' {
  export class Unicode11Addon {
    constructor();
    activate(terminal: { unicode: { register(provider: unknown): void } }): void;
    dispose(): void;
  }
}
