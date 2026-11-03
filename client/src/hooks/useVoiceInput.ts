import { useCallback, useEffect, useRef, useState } from 'react';

/**
 * Optional browser speech-input adapter. The result is plain editable text:
 * callers MUST use the same reviewed submit / approval flow as typed prompts.
 * Never assume that SpeechRecognition exists (notably on mobile browsers).
 */
type RecognitionResult = {
  isFinal: boolean;
  [index: number]: { transcript: string };
};
type RecognitionEvent = {
  resultIndex: number;
  results: ArrayLike<RecognitionResult>;
};
type RecognitionErrorEvent = { error: string };
type Recognition = {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  onresult: ((event: RecognitionEvent) => void) | null;
  onerror: ((event: RecognitionErrorEvent) => void) | null;
  onend: (() => void) | null;
  start(): void;
  stop(): void;
  abort(): void;
};
type RecognitionConstructor = new () => Recognition;
type SpeechWindow = Window & {
  SpeechRecognition?: RecognitionConstructor;
  webkitSpeechRecognition?: RecognitionConstructor;
};

export type VoiceInputState = 'unsupported' | 'idle' | 'listening' | 'error';

function recognitionClass(): RecognitionConstructor | undefined {
  if (typeof window === 'undefined') return undefined;
  const browser = window as SpeechWindow;
  return browser.SpeechRecognition ?? browser.webkitSpeechRecognition;
}

const errorLabels: Record<string, string> = {
  'not-allowed': '마이크 권한을 허용해 주세요.',
  'service-not-allowed': '음성 인식 서비스 사용이 허용되지 않았습니다.',
  'no-speech': '음성이 감지되지 않았습니다. 다시 시도해 주세요.',
  'audio-capture': '마이크에 접근할 수 없습니다.',
  'network': '음성 인식 연결에 실패했습니다.',
  'language-not-supported': '현재 언어의 음성 인식을 지원하지 않습니다.',
};

export function useVoiceInput(language = 'ko-KR') {
  const [state, setState] = useState<VoiceInputState>(() => recognitionClass() ? 'idle' : 'unsupported');
  const [finalText, setFinalText] = useState('');
  const [interimText, setInterimText] = useState('');
  const [error, setError] = useState('');
  const instanceRef = useRef<Recognition | null>(null);

  const stop = useCallback(() => {
    instanceRef.current?.stop();
  }, []);

  const reset = useCallback(() => {
    instanceRef.current?.abort();
    instanceRef.current = null;
    setFinalText('');
    setInterimText('');
    setError('');
    setState(recognitionClass() ? 'idle' : 'unsupported');
  }, []);

  const start = useCallback(() => {
    const Constructor = recognitionClass();
    if (!Constructor) {
      setState('unsupported');
      setError('이 브라우저는 음성 인식을 지원하지 않습니다. 텍스트 입력을 사용해 주세요.');
      return;
    }
    instanceRef.current?.abort();
    const recognizer = new Constructor();
    instanceRef.current = recognizer;
    recognizer.lang = language;
    recognizer.continuous = true;
    recognizer.interimResults = true;
    setFinalText('');
    setInterimText('');
    setError('');
    recognizer.onresult = (event) => {
      if (instanceRef.current !== recognizer) return;
      let newlyFinal = '';
      let interim = '';
      for (let i = event.resultIndex; i < event.results.length; i += 1) {
        const result = event.results[i];
        if (result.isFinal) newlyFinal += result[0]?.transcript ?? '';
        else interim += result[0]?.transcript ?? '';
      }
      // Finalized results are appended once; the browser normally advances
      // resultIndex for new results, but this adapter never submits on its own.
      if (newlyFinal) setFinalText((prev) => [prev, newlyFinal.trim()].filter(Boolean).join(' '));
      setInterimText(interim.trim());
    };
    recognizer.onerror = (event) => {
      if (instanceRef.current !== recognizer) return;
      setError(errorLabels[event.error] ?? `음성 인식 오류: ${event.error}`);
      setState('error');
    };
    recognizer.onend = () => {
      if (instanceRef.current !== recognizer) return;
      instanceRef.current = null;
      setInterimText('');
      setState((current) => current === 'error' ? current : 'idle');
    };
    try {
      recognizer.start();
      setState('listening');
    } catch {
      instanceRef.current = null;
      setState('error');
      setError('음성 인식을 시작하지 못했습니다. 마이크 권한을 확인해 주세요.');
    }
  }, [language]);

  useEffect(() => () => {
    instanceRef.current?.abort();
    instanceRef.current = null;
  }, []);

  return {
    supported: state !== 'unsupported',
    state,
    finalText,
    interimText,
    error,
    start,
    stop,
    reset,
  };
}
