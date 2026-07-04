import { useState, useRef, useCallback, useEffect } from 'react';
import { agentDeckWS } from '../../lib/ws';
import { api } from '../../lib/api';

interface SlashCommand {
  name: string;
  type: string;
  description?: string;
}

/**
 * How Prompt Bar text is delivered to the terminal. The server (ws/hub.go)
 * performs the actual wrapping; the client only picks the mode.
 *   bracketed-paste — ESC[200~ … ESC[201~  (default, keeps multi-line intact)
 *   plain-paste     — raw text
 *   typewriter      — raw text (reserved for future char-by-char client pacing)
 */
export type PromptSubmitMode = 'bracketed-paste' | 'plain-paste' | 'typewriter';

const PROMPT_SUBMIT_MODE: PromptSubmitMode = 'bracketed-paste';

interface PromptBarProps {
  agentId: string;
  /** Called after Send / Paste / Esc — desktop refocuses the terminal, mobile closes the bar. */
  onDone: () => void;
  /** Fired when the textarea gains/loses focus so the terminal can suspend auto-focus. */
  onFocusChange?: (focused: boolean) => void;
  /** Mobile: focus the textarea as soon as the bar opens. */
  autoFocus?: boolean;
  /** Mobile: show a close (✕) affordance. */
  showClose?: boolean;
}

const TYPE_LABELS: Record<string, string> = {
  command: 'cmd',
  agent: 'agent',
  skill: 'skill',
};

export function PromptBar({ agentId, onDone, onFocusChange, autoFocus, showClose }: PromptBarProps) {
  const [value, setValue] = useState('');
  const [commands, setCommands] = useState<SlashCommand[]>([]);
  const [suggestions, setSuggestions] = useState<SlashCommand[]>([]);
  const [selectedIdx, setSelectedIdx] = useState(0);
  const [focused, setFocused] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const lineCount = value.split('\n').length;

  // Fetch slash commands once
  useEffect(() => {
    api.slashCommands()
      .then((data) => { if (Array.isArray(data)) setCommands(data); })
      .catch(() => {});
  }, []);

  // Autofocus when opened (mobile)
  useEffect(() => {
    if (autoFocus) textareaRef.current?.focus();
  }, [autoFocus]);

  // Update slash / @ suggestions as the user types
  useEffect(() => {
    if (value.startsWith('/') || value.startsWith('@')) {
      const q = value.toLowerCase();
      const matched = commands.filter((c) => c.name.toLowerCase().startsWith(q));
      setSuggestions(matched.slice(0, 8));
      setSelectedIdx(0);
    } else {
      setSuggestions([]);
    }
  }, [value, commands]);

  // Send text to the terminal. submit=true appends Enter, submit=false just pastes.
  const dispatch = useCallback((submit: boolean) => {
    if (!value) return;
    agentDeckWS.send(submit ? 'terminal:pasteSubmit' : 'terminal:pasteOnly', {
      agentId,
      text: value,
      mode: PROMPT_SUBMIT_MODE,
    });
    setValue('');
    setSuggestions([]);
    onDone();
  }, [value, agentId, onDone]);

  const applySuggestion = useCallback((cmd: string) => {
    setValue(cmd + ' ');
    setSuggestions([]);
    textareaRef.current?.focus();
  }, []);

  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // Autocomplete navigation takes priority while the list is open
    if (suggestions.length > 0) {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setSelectedIdx((i) => Math.min(i + 1, suggestions.length - 1));
        return;
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault();
        setSelectedIdx((i) => Math.max(i - 1, 0));
        return;
      }
      if (e.key === 'Tab' || (e.key === 'Enter' && value.length < (suggestions[selectedIdx]?.name.length ?? 0))) {
        e.preventDefault();
        applySuggestion(suggestions[selectedIdx].name);
        return;
      }
      if (e.key === 'Escape') {
        e.preventDefault();
        setSuggestions([]);
        return;
      }
    }

    // Cmd/Ctrl+Enter → Send
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      dispatch(true);
      return;
    }

    // Enter → Send, Shift+Enter → newline (default textarea behavior)
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      dispatch(true);
      return;
    }

    // Esc → hand focus back to the terminal / close the bar
    if (e.key === 'Escape') {
      e.preventDefault();
      onDone();
    }
  }, [suggestions, selectedIdx, value, applySuggestion, dispatch, onDone]);

  return (
    <div className="relative">
      {/* Slash command autocomplete */}
      {suggestions.length > 0 && (
        <div className="absolute bottom-full left-0 right-0 max-h-48 overflow-y-auto shadow-xl z-20 bg-deck-surface border border-deck-border">
          {suggestions.map((cmd, i) => (
            <button
              key={cmd.name}
              onClick={() => applySuggestion(cmd.name)}
              className={`w-full text-left px-3 py-1.5 flex items-center gap-2 text-sm transition-colors ${
                i === selectedIdx ? 'bg-deck-accent/20 text-deck-text' : 'text-deck-text-dim hover:bg-deck-border/30'
              }`}
            >
              <span className="font-mono">{cmd.name}</span>
              <span className="text-[10px] px-1.5 py-0.5 rounded bg-deck-bg text-deck-text-dim">
                {TYPE_LABELS[cmd.type] || cmd.type}
              </span>
              {cmd.description && (
                <span className="text-xs truncate ml-auto text-deck-text-dim">{cmd.description}</span>
              )}
            </button>
          ))}
        </div>
      )}

      {/* Prompt input row */}
      <div className="flex items-start gap-2 px-3 py-2.5 md:gap-1.5 md:px-2 md:py-1.5 safe-bottom bg-deck-bg border-t border-deck-border/50">
        <span className="text-sm shrink-0 font-mono mt-1.5 md:text-xs text-deck-accent">&gt;</span>

        <textarea
          ref={textareaRef}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={handleKeyDown}
          onFocus={() => { setFocused(true); onFocusChange?.(true); }}
          onBlur={() => { setFocused(false); onFocusChange?.(false); }}
          placeholder="한글·긴 프롬프트를 입력하고 Send ( / 슬래시 커맨드, Shift+Enter 줄바꿈)"
          autoComplete="off"
          autoCorrect="off"
          autoCapitalize="off"
          spellCheck={false}
          rows={focused || value ? Math.min(Math.max(lineCount, 1), 10) : 1}
          className="flex-1 min-w-0 bg-transparent text-base outline-none font-mono resize-none text-deck-text md:text-sm"
          style={{ caretColor: 'var(--deck-accent, #6366f1)' }}
        />

        <div className="flex items-center gap-1.5 shrink-0 mt-0.5">
          {showClose && (
            <button
              onMouseDown={(e) => { e.preventDefault(); onDone(); }}
              className="px-2 py-2 rounded text-sm touch-manipulation active:opacity-70 bg-deck-surface text-deck-text-dim md:px-2 md:py-1 md:text-xs"
              title="Close (Esc)"
            >
              ✕
            </button>
          )}
          <button
            onClick={() => dispatch(false)}
            disabled={!value}
            className="px-3 py-2 rounded text-sm font-medium shrink-0 touch-manipulation active:opacity-70 disabled:opacity-40
                       bg-deck-surface text-deck-text-dim md:px-2.5 md:py-1 md:text-xs"
            title="Paste into terminal without Enter"
          >
            Paste
          </button>
          <button
            onClick={() => dispatch(true)}
            disabled={!value}
            className="btn-primary px-4 py-2 rounded text-sm font-medium shrink-0 touch-manipulation active:opacity-70 disabled:opacity-40
                       md:px-2.5 md:py-1 md:text-xs"
            title="Paste into terminal and send (Enter)"
          >
            Send
          </button>
        </div>
      </div>
    </div>
  );
}
