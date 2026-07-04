import { useState, useRef, useCallback, useEffect } from 'react';
import { agentDeckWS } from '../../lib/ws';

/**
 * How Prompt Bar text is delivered to the terminal. The server (ws/hub.go)
 * performs the actual wrapping; the client only picks the mode.
 *   bracketed-paste — ESC[200~ … ESC[201~  (default, keeps multi-line intact)
 *   plain-paste     — raw text
 */
export type PromptSubmitMode = 'bracketed-paste' | 'plain-paste';

const PROMPT_SUBMIT_MODE: PromptSubmitMode = 'bracketed-paste';

interface PromptBarProps {
  agentId: string;
  /** Touch devices (mobile / iPad): the bar is mandatory — offer collapse, not close. */
  forced: boolean;
  /** Collapsed = only the expand affordance shows (forced mode only). */
  collapsed: boolean;
  /** Toggle collapse (forced mode). */
  onToggleCollapse: () => void;
  /** Desktop only: fully close the bar. */
  onClose: () => void;
  /** Move focus into the terminal (after Send / Paste / 터미널 조작). */
  onFocusTerminal: () => void;
  /** Fired when the textarea gains/loses focus so the terminal suspends auto-focus. */
  onFocusChange?: (focused: boolean) => void;
  /** Focus the textarea as soon as the bar mounts / expands. */
  autoFocus?: boolean;
}

/**
 * Prompt Bar — a plain textarea for Korean / long / multi-line prompts. Text is
 * composed here (where the browser's native IME works correctly) and then
 * pasted into the current terminal. It does NOT interpret Claude state, handle
 * approvals, or replace terminal control keys — it only pastes text.
 */
export function PromptBar({
  agentId,
  forced,
  collapsed,
  onToggleCollapse,
  onClose,
  onFocusTerminal,
  onFocusChange,
  autoFocus,
}: PromptBarProps) {
  const [value, setValue] = useState('');
  const [focused, setFocused] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  // Guards Enter-to-Send while a Korean/IME syllable is still composing.
  const isComposingRef = useRef(false);

  const lineCount = value.split('\n').length;

  // Focus the textarea on mount / when expanded.
  useEffect(() => {
    if (collapsed) return;
    if (autoFocus) textareaRef.current?.focus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [collapsed]);

  // Send text to the terminal. submit=true appends Enter, submit=false just pastes.
  const dispatch = useCallback((submit: boolean) => {
    const text = value;
    if (!text) return;
    agentDeckWS.send(submit ? 'terminal:pasteSubmit' : 'terminal:pasteOnly', {
      agentId,
      text,
      mode: PROMPT_SUBMIT_MODE,
    });
    setValue('');
    onFocusTerminal();
  }, [value, agentId, onFocusTerminal]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // Never treat Enter as Send while an IME syllable is composing.
    if (e.nativeEvent.isComposing || isComposingRef.current) return;

    // Cmd/Ctrl+Enter → Send, Enter → Send, Shift+Enter → newline.
    if (e.key === 'Enter' && ((e.metaKey || e.ctrlKey) || !e.shiftKey)) {
      e.preventDefault();
      dispatch(true);
      return;
    }

    // Esc → forced: collapse + focus terminal; desktop: close + focus terminal.
    if (e.key === 'Escape') {
      e.preventDefault();
      if (forced) onToggleCollapse();
      else onClose();
      onFocusTerminal();
    }
  }, [forced, dispatch, onToggleCollapse, onClose, onFocusTerminal]);

  // Collapsed (forced mode): a full-width tap target to reopen the input.
  if (forced && collapsed) {
    return (
      <div className="px-3 py-2 bg-deck-bg border-t border-deck-border/50">
        <button
          onMouseDown={(e) => { e.preventDefault(); onToggleCollapse(); }}
          className="flex w-full items-center gap-2 px-4 py-3 rounded-xl text-sm font-medium touch-manipulation active:opacity-70 bg-deck-accent/15 text-deck-accent"
        >
          <span className="text-base leading-none">⌨</span>
          한글·프롬프트 입력
          <span className="ml-auto text-xs opacity-70">펼치기 ▲</span>
        </button>
      </div>
    );
  }

  return (
    <div className="bg-deck-bg border-t border-deck-border/50">
      {/* Header: role label + collapse/close affordance */}
      <div className="flex items-center gap-2 px-3 pt-2 pb-1.5">
        <span className="text-xs font-semibold text-deck-accent">Prompt</span>
        <span className="hidden md:inline text-[11px] text-deck-text-dim truncate">한글/긴 프롬프트를 입력하세요</span>
        <button
          onMouseDown={(e) => { e.preventDefault(); if (forced) onToggleCollapse(); else onClose(); }}
          className="ml-auto shrink-0 px-2.5 py-1 rounded-md text-xs touch-manipulation active:opacity-70 bg-deck-surface text-deck-text-dim"
          title={forced ? '접기' : '닫기 (Esc)'}
        >
          {forced ? '접기 ▼' : '닫기 ✕'}
        </button>
      </div>

      {/* Input — textarea full width, buttons stacked below on mobile and
          inline on desktop. */}
      <div className="flex flex-col gap-2 px-3 pb-3 md:flex-row md:items-end md:gap-2 md:pb-2">
        <textarea
          ref={textareaRef}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={handleKeyDown}
          onCompositionStart={() => { isComposingRef.current = true; }}
          onCompositionEnd={() => { isComposingRef.current = false; }}
          onFocus={() => { setFocused(true); onFocusChange?.(true); }}
          onBlur={() => { setFocused(false); onFocusChange?.(false); }}
          placeholder={forced ? '메시지를 입력하세요' : '한글·긴 프롬프트 입력 (Shift+Enter 줄바꿈)'}
          autoComplete="off"
          autoCorrect="off"
          autoCapitalize="off"
          spellCheck={false}
          inputMode="text"
          rows={focused || value ? Math.min(Math.max(lineCount, 1), forced ? 5 : 10) : 1}
          className="w-full md:flex-1 min-w-0 rounded-xl bg-deck-surface px-3.5 py-2.5 text-base outline-none resize-none
                     text-deck-text placeholder:text-deck-text-dim/60 border border-deck-border/60
                     focus:border-deck-accent/60 md:text-sm"
          style={{ caretColor: 'var(--deck-accent, #6366f1)' }}
        />

        <div className="flex items-center gap-2 md:gap-1.5 md:shrink-0">
          <button
            onMouseDown={(e) => { e.preventDefault(); setValue(''); textareaRef.current?.focus(); }}
            disabled={!value}
            className="px-3 py-2 rounded-lg text-sm shrink-0 touch-manipulation active:opacity-70 disabled:opacity-40
                       bg-deck-surface text-deck-text-dim md:px-2 md:py-1.5 md:text-xs"
            title="입력 내용 지우기"
          >
            지우기
          </button>
          <button
            onMouseDown={(e) => { e.preventDefault(); onFocusTerminal(); }}
            className="px-3 py-2 rounded-lg text-sm shrink-0 touch-manipulation active:opacity-70
                       bg-deck-surface text-deck-text-dim md:px-2 md:py-1.5 md:text-xs"
            title="터미널로 focus 이동 (방향키·승인 조작)"
          >
            터미널
          </button>
          {/* Push the primary actions to the right on mobile. */}
          <div className="flex-1 md:hidden" />
          <button
            onMouseDown={(e) => { e.preventDefault(); dispatch(false); }}
            disabled={!value}
            className="px-3 py-2 rounded-lg text-sm font-medium shrink-0 touch-manipulation active:opacity-70 disabled:opacity-40
                       bg-deck-surface text-deck-text-dim md:px-2.5 md:py-1.5 md:text-xs"
            title="터미널에 붙여넣기 (Enter 없음)"
          >
            붙여넣기
          </button>
          <button
            onMouseDown={(e) => { e.preventDefault(); dispatch(true); }}
            disabled={!value}
            className="btn-primary px-5 py-2 rounded-lg text-sm font-semibold shrink-0 touch-manipulation active:opacity-70 disabled:opacity-40
                       md:px-3 md:py-1.5 md:text-xs"
            title="터미널에 붙여넣고 Enter 전송 (⌘/Ctrl+Enter)"
          >
            전송
          </button>
        </div>
      </div>
    </div>
  );
}
