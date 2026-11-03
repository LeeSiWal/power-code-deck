import { useCallback, useEffect, useRef, useState } from 'react';

/**
 * Experimental acoustic barge-in detector.
 *
 * Web SpeechRecognition cannot simultaneously listen reliably while Safari TTS
 * plays. This uses a separate microphone MediaStream and Web Audio RMS gate to
 * notice sustained nearby speech and interrupt TTS, then the existing speech
 * recognizer captures the spoken instruction. This is NOT speaker verification
 * or a full speech VAD. Speakers may leak TTS into the microphone even with
 * echo cancellation; headphones are strongly recommended.
 */
export function useAcousticBargeIn(onSpeech: () => void) {
  const handlerRef = useRef(onSpeech);
  handlerRef.current = onSpeech;
  const streamRef = useRef<MediaStream | null>(null);
  const contextRef = useRef<AudioContext | null>(null);
  const frameRef = useRef<number | null>(null);
  const [active, setActive] = useState(false);
  const [error, setError] = useState('');
  const detectingRef = useRef(false);
  const trippedRef = useRef(false);

  const stop = useCallback(() => {
    detectingRef.current = false;
    trippedRef.current = false;
    if (frameRef.current !== null) cancelAnimationFrame(frameRef.current);
    frameRef.current = null;
    streamRef.current?.getTracks().forEach((track) => track.stop());
    streamRef.current = null;
    const context = contextRef.current;
    contextRef.current = null;
    if (context) void context.close().catch(() => {});
    setActive(false);
  }, []);

  const start = useCallback(async () => {
    stop();
    setError('');
    if (!navigator.mediaDevices?.getUserMedia) {
      setError('브라우저가 동시 마이크 감지를 지원하지 않아. 응답 중단 버튼을 이용해 줘.');
      return false;
    }
    try {
      // Request within the initiating click; Safari may reject lazy requests.
      const stream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
      });
      const AudioContextClass = window.AudioContext;
      if (!AudioContextClass) {
        stream.getTracks().forEach((track) => track.stop());
        setError('오디오 분석을 지원하지 않는 브라우저야.');
        return false;
      }
      const context = new AudioContextClass();
      streamRef.current = stream;
      contextRef.current = context;
      const source = context.createMediaStreamSource(stream);
      const highpass = context.createBiquadFilter();
      highpass.type = 'highpass';
      highpass.frequency.value = 180;
      const analyser = context.createAnalyser();
      analyser.fftSize = 1024;
      source.connect(highpass);
      highpass.connect(analyser);
      // Never connect the live mic to speakers, to avoid feedback.
      await context.resume();
      const samples = new Float32Array(analyser.fftSize);
      let elevatedSince = 0;
      let lastTrip = 0;
      const frame = (time: number) => {
        if (streamRef.current !== stream) return;
        analyser.getFloatTimeDomainData(samples);
        let sum = 0;
        for (let i = 0; i < samples.length; i++) sum += samples[i] * samples[i];
        const rms = Math.sqrt(sum / samples.length);
        // Conservative gate: sustained energy, not momentary clicks. This
        // cannot conclusively discriminate the user's voice from loud TTS.
        if (detectingRef.current && !trippedRef.current && time - lastTrip > 1000) {
          if (rms > 0.075) {
            if (!elevatedSince) elevatedSince = time;
            if (time - elevatedSince > 270) {
              lastTrip = time;
              trippedRef.current = true;
              detectingRef.current = false;
              elevatedSince = 0;
              handlerRef.current();
            }
          } else {
            elevatedSince = 0;
          }
        } else {
          elevatedSince = 0;
        }
        frameRef.current = requestAnimationFrame(frame);
      };
      frameRef.current = requestAnimationFrame(frame);
      setActive(true);
      return true;
    } catch (err) {
      stop();
      setError(err instanceof Error ? `마이크 상시 감지를 시작하지 못했어: ${err.message}` : '마이크 접근이 거부됐어.');
      return false;
    }
  }, [stop]);

  const setSpeaking = useCallback((speaking: boolean) => {
    detectingRef.current = speaking;
    if (!speaking) trippedRef.current = false;
  }, []);

  useEffect(() => () => {
    if (frameRef.current !== null) cancelAnimationFrame(frameRef.current);
    streamRef.current?.getTracks().forEach((track) => track.stop());
    if (contextRef.current) void contextRef.current.close().catch(() => {});
  }, []);

  return { active, error, start, stop, setSpeaking };
}
