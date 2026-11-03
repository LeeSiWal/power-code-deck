import { useCallback, useEffect, useRef, useState } from 'react';

// Privacy-first recording adapter for explicit speaker enrollment. Audio stays
// in memory until the user explicitly submits enrollment and is then released.
// This is separate from browser SpeechRecognition, which does not expose audio.
export type RecordingState = 'idle' | 'recording' | 'processing' | 'error';

function encodeWav(samples: Float32Array, rate: number): Blob {
  const buffer = new ArrayBuffer(44 + samples.length * 2);
  const view = new DataView(buffer);
  const write = (offset: number, value: string) => {
    for (let i = 0; i < value.length; i++) view.setUint8(offset + i, value.charCodeAt(i));
  };
  write(0, 'RIFF');
  view.setUint32(4, buffer.byteLength - 8, true);
  write(8, 'WAVE');
  write(12, 'fmt ');
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, rate, true);
  view.setUint32(28, rate * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  write(36, 'data');
  view.setUint32(40, samples.length * 2, true);
  for (let i = 0; i < samples.length; i++) {
    const sample = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(44 + i * 2, sample < 0 ? sample * 32768 : sample * 32767, true);
  }
  return new Blob([buffer], { type: 'audio/wav' });
}

export function useVoiceRecorder() {
  const [state, setState] = useState<RecordingState>('idle');
  const [error, setError] = useState('');
  const contextRef = useRef<AudioContext | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const processorRef = useRef<ScriptProcessorNode | null>(null);
  const sourceRef = useRef<MediaStreamAudioSourceNode | null>(null);
  const buffersRef = useRef<Float32Array[]>([]);
  const countRef = useRef(0);
  const startedRef = useRef(0);

  const release = useCallback(() => {
    if (processorRef.current) {
      processorRef.current.onaudioprocess = null;
      processorRef.current.disconnect();
      processorRef.current = null;
    }
    sourceRef.current?.disconnect();
    sourceRef.current = null;
    streamRef.current?.getTracks().forEach((track) => track.stop());
    streamRef.current = null;
    const context = contextRef.current;
    contextRef.current = null;
    if (context) void context.close().catch(() => {});
  }, []);

  const start = useCallback(async () => {
    if (state === 'recording') return;
    if (!navigator.mediaDevices?.getUserMedia || !window.AudioContext) {
      setError('이 브라우저는 마이크 녹음을 지원하지 않아.');
      setState('error');
      return;
    }
    release();
    buffersRef.current = [];
    countRef.current = 0;
    setError('');
    try {
      const stream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, channelCount: 1 },
      });
      const context = new AudioContext();
      streamRef.current = stream;
      contextRef.current = context;
      await context.resume();
      const source = context.createMediaStreamSource(stream);
      // ScriptProcessor is an MVP fallback supported by Safari. The processor
      // sends silence to output to stay scheduled, not microphone playback.
      const processor = context.createScriptProcessor(4096, 1, 1);
      processor.onaudioprocess = (event) => {
        const input = event.inputBuffer.getChannelData(0);
        if (countRef.current + input.length > context.sampleRate * 15) return;
        buffersRef.current.push(new Float32Array(input));
        countRef.current += input.length;
        event.outputBuffer.getChannelData(0).fill(0);
      };
      source.connect(processor);
      processor.connect(context.destination);
      sourceRef.current = source;
      processorRef.current = processor;
      startedRef.current = performance.now();
      setState('recording');
    } catch {
      release();
      setError('마이크 권한이나 오디오 장치를 확인해 줘.');
      setState('error');
    }
  }, [state, release]);

  const stop = useCallback((): Blob | null => {
    if (state !== 'recording') return null;
    setState('processing');
    const rate = contextRef.current?.sampleRate ?? 48000;
    const duration = countRef.current / rate;
    release();
    if (duration < 1.5 || duration > 15 || performance.now() - startedRef.current > 18000) {
      buffersRef.current = [];
      setState('error');
      setError('목소리는 2초에서 15초 사이로 녹음해 줘.');
      return null;
    }
    const all = new Float32Array(countRef.current);
    let offset = 0;
    for (const chunk of buffersRef.current) {
      all.set(chunk, offset);
      offset += chunk.length;
    }
    buffersRef.current = [];
    const wav = encodeWav(all, rate);
    all.fill(0);
    setState('idle');
    if (wav.size > 4 * 1024 * 1024) {
      setState('error');
      setError('녹음 크기가 너무 커. 더 짧게 녹음해 줘.');
      return null;
    }
    return wav;
  }, [state, release]);

  const reset = useCallback(() => {
    release();
    buffersRef.current = [];
    countRef.current = 0;
    setState('idle');
    setError('');
  }, [release]);

  useEffect(() => () => {
    release();
    buffersRef.current = [];
  }, [release]);

  return { state, error, start, stop, reset };
}
