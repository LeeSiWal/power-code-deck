import { useCallback, useEffect, useRef, useState } from 'react';
import { toVoiceNarration } from '../lib/voiceNarration';

// Browser TTS is optional and independent of the coding provider. Audio is
// disabled by default so a reopened private coding session never speaks aloud.
export function useVoicePlayback() {
  const supported = typeof window !== 'undefined'
    && 'speechSynthesis' in window && typeof window.SpeechSynthesisUtterance !== 'undefined';
  const [enabled, setEnabled] = useState(false);
  // A newly enabled conversation can speak in the same event turn before
  // React re-renders: read the authoritative ref instead of stale state.
  const enabledRef = useRef(false);
  const [speaking, setSpeaking] = useState(false);
  const activeRef = useRef<SpeechSynthesisUtterance | null>(null);

  const stop = useCallback(() => {
    if (supported) window.speechSynthesis.cancel();
    activeRef.current = null;
    setSpeaking(false);
  }, [supported]);

  const speak = useCallback((text: string) => {
    if (!supported || !enabledRef.current || !text.trim()) return false;
    stop();
    // Audio is a distinct, concise narration derived from the complete
    // visible answer; never overwrite the assistant's on-screen content.
    const trimmed = toVoiceNarration(text).text;
    if (!trimmed) return false;
    const utterance = new SpeechSynthesisUtterance(trimmed);
    utterance.lang = 'ko-KR';
    utterance.rate = 1.05;
    utterance.onend = utterance.onerror = () => {
      if (activeRef.current === utterance) {
        activeRef.current = null;
        setSpeaking(false);
      }
    };
    activeRef.current = utterance;
    setSpeaking(true);
    window.speechSynthesis.speak(utterance);
    return true;
  }, [supported, stop]);

  const toggle = useCallback(() => {
    const next = !enabledRef.current;
    if (!next) stop();
    enabledRef.current = next;
    setEnabled(next);
  }, [stop]);

  // Cancel speech when changing session or unmounting; no background audio.
  useEffect(() => () => {
    enabledRef.current = false;
    if (supported) window.speechSynthesis.cancel();
    activeRef.current = null;
  }, [supported]);

  return { supported, enabled, speaking, speak, stop, toggle };
}
