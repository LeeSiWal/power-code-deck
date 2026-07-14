import { useRef, useCallback, useImperativeHandle, forwardRef, type CSSProperties, type HTMLAttributes } from 'react';
import { CustomTerm } from '../../lib/customTerm/CustomTerm';

export interface CustomTerminalHandle {
  write(data: string | Uint8Array): void;
  resize(cols: number, rows: number): void;
  focus(): void;
}

interface CustomTerminalProps extends Omit<HTMLAttributes<HTMLDivElement>, 'onResize'> {
  cols?: number;
  rows?: number;
  autoResize?: boolean;
  cursorBlink?: boolean;
  onData?: (data: string) => void;
  onResize?: (cols: number, rows: number) => void;
  onTitle?: (title: string) => void;
  onReady?: (term: CustomTerm) => void;
  className?: string;
  style?: CSSProperties;
}

/**
 * React wrapper around CustomTerm (xterm-headless parser + our own DOM renderer).
 * Same handle (write/resize/focus) and onReady(term) contract TerminalView expects.
 */
export const CustomTerminal = forwardRef<CustomTerminalHandle, CustomTerminalProps>(function CustomTerminal(
  { cols = 80, rows = 24, autoResize = false, cursorBlink = false, onData, onResize, onTitle, onReady, className, style, ...htmlProps },
  ref,
) {
  const termRef = useRef<CustomTerm | null>(null);
  const cbRef = useRef({ onData, onResize, onTitle, onReady });
  cbRef.current = { onData, onResize, onTitle, onReady };

  useImperativeHandle(ref, () => ({
    write: (data) => termRef.current?.write(data),
    resize: (c, r) => termRef.current?.resize(c, r),
    focus: () => termRef.current?.focus(),
  }));

  const containerRef = useCallback((el: HTMLDivElement | null) => {
    if (!el) return;
    const term = new CustomTerm(el, {
      cols, rows, autoResize, cursorBlink,
      onData: (d) => cbRef.current.onData?.(d),
      onResize: (c, r) => cbRef.current.onResize?.(c, r),
      onTitle: (t) => cbRef.current.onTitle?.(t),
    });
    termRef.current = term;
    term.init();
    cbRef.current.onReady?.(term);
    return () => { term.destroy(); termRef.current = null; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div
      ref={containerRef}
      className={['wterm', className].filter(Boolean).join(' ')}
      style={style}
      role="textbox"
      aria-label="Terminal"
      aria-multiline="true"
      {...htmlProps}
    />
  );
});
