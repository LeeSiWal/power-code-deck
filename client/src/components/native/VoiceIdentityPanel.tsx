import { useCallback, useEffect, useState } from 'react';
import { apiRequest } from '../../lib/api';
import { useVoiceRecorder } from '../../hooks/useVoiceRecorder';

type ProfileStatus = {
  available: boolean;
  enabled: boolean;
  enrolled: boolean;
  persistent: boolean;
  commandGate: boolean;
};

const prompts = [
  '오늘은 음성 대화 기능을 시험하고 있습니다.',
  '지금 진행 중인 작업 상태를 확인해 주세요.',
  '목소리 등록을 안전하게 완료하고 싶습니다.',
];

async function parseResponse(response: Response): Promise<any> {
  const type = response.headers.get('content-type') || '';
  if (!type.includes('application/json')) throw new Error('서버에서 음성 등록 기능을 제공하지 않아.');
  const payload = await response.json();
  if (!response.ok) throw new Error(payload.error || '요청에 실패했어.');
  return payload;
}

/**
 * Experimental enrollment UI. Does not grant coding permissions.
 * Always keep real authorization and approvals in the normal coding screen.
 */
export function VoiceIdentityPanel() {
  const recorder = useVoiceRecorder();
  const [status, setStatus] = useState<ProfileStatus | null>(null);
  const [samples, setSamples] = useState<Blob[]>([]);
  const [consent, setConsent] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [verified, setVerified] = useState<boolean | null>(null);

  const refresh = useCallback(async () => {
    try {
      const response = await apiRequest('/voice/status');
      const result = await parseResponse(response) as ProfileStatus;
      setStatus(result);
    } catch {
      setStatus(null);
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);

  const capture = async () => {
    setVerified(null);
    if (recorder.state === 'recording') {
      const sample = recorder.stop();
      if (sample) setSamples((all) => [...all, sample].slice(0, 3));
    } else {
      await recorder.start();
    }
  };

  const enroll = async () => {
    if (!consent || samples.length !== 3 || busy) return;
    setBusy(true);
    setMessage('');
    try {
      const form = new FormData();
      form.set('consent', 'yes');
      samples.forEach((audio, i) => form.set(`sample${i + 1}`, audio, `speaker-${i + 1}.wav`));
      await parseResponse(await apiRequest('/voice/enroll', { method: 'POST', body: form }));
      setSamples([]);
      setConsent(false);
      setMessage('목소리 특징 등록에 성공했어. 원본 녹음은 화면에서 지웠어.');
      await refresh();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : '등록에 실패했어.');
    } finally {
      setBusy(false);
    }
  };

  const verify = async () => {
    if (busy) return;
    if (recorder.state === 'recording') {
      const sample = recorder.stop();
      if (!sample) return;
      setBusy(true);
      try {
        const result = await parseResponse(await apiRequest('/voice/verify', {
          method: 'POST', headers: { 'Content-Type': 'audio/wav' }, body: sample,
        })) as { verified: boolean };
        setVerified(result.verified);
      } catch (err) {
        setMessage(err instanceof Error ? err.message : '검증에 실패했어.');
        setVerified(false);
      } finally {
        setBusy(false);
      }
    } else {
      await recorder.start();
    }
  };

  const deleteProfile = async () => {
    if (busy || !window.confirm('등록된 목소리 특징을 삭제할까?')) return;
    setBusy(true);
    try {
      await parseResponse(await apiRequest('/voice/profile', { method: 'DELETE' }));
      setVerified(null);
      setSamples([]);
      setMessage('등록된 목소리 정보를 삭제했어.');
      await refresh();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : '삭제에 실패했어.');
    } finally {
      setBusy(false);
    }
  };

  if (!status) return null; // Feature disabled or server not supporting it.
  return (
    <section className="rounded-xl border border-deck-border bg-deck-surface p-3 space-y-3" aria-label="실험적 목소리 등록">
      <div>
        <h2 className="font-semibold text-sm">목소리 등록 · 실험 기능</h2>
        <p className="text-xs text-deck-text-dim mt-1">
          목소리 일치 여부만 확인해. 코딩 실행 권한으로 사용하지 않으며 녹음 재생·복제 음성을 완벽히 차단하지 못해.
          서버 재시작 시 등록 정보는 사라져.
        </p>
      </div>
      {status.enrolled ? (
        <>
          <p className="text-xs">등록 상태: {status.enabled ? '등록됨 · 비교 가능' : '등록됐으나 비활성'}</p>
          <button type="button" disabled={busy}
            onClick={verify} className="min-h-11 px-3 rounded-lg border border-deck-border text-sm disabled:opacity-40">
            {recorder.state === 'recording' ? '검증 음성 녹음 종료' : '내 목소리 검증하기'}
          </button>
          {verified !== null && <p className="text-sm" role="status">{verified ? '목소리 특징이 일치해.' : '목소리 확인에 실패했어.'}</p>}
          <button type="button" disabled={busy} onClick={deleteProfile}
            className="min-h-11 px-3 rounded-lg border border-deck-border text-sm disabled:opacity-40">목소리 정보 삭제</button>
        </>
      ) : (
        <>
          <p className="text-xs text-deck-text-dim">서로 다른 문장을 2~15초씩 세 번 읽어 줘. 마이크 버튼을 다시 누르면 현재 녹음을 종료해.</p>
          <p className="text-sm">{samples.length < 3 ? prompts[samples.length] : '세 번의 녹음을 완료했어.'}</p>
          <div className="flex items-center gap-2">
            <button type="button" disabled={busy || samples.length >= 3} onClick={capture}
              className="min-h-11 px-3 rounded-lg border border-deck-border text-sm disabled:opacity-40">
              {recorder.state === 'recording' ? '■ 녹음 종료' : '🎙 녹음 시작'} ({samples.length}/3)
            </button>
            <button type="button" onClick={() => { recorder.reset(); setSamples([]); setMessage(''); }}
              className="min-h-11 px-3 rounded-lg border border-deck-border text-xs">다시 녹음</button>
          </div>
          <label className="flex items-start gap-2 text-xs">
            <input type="checkbox" checked={consent} onChange={(event) => setConsent(event.target.checked)} />
            목소리에서 특징 정보를 추출하고 서버 메모리에 임시 보관하는 데 동의해.
          </label>
          <button type="button" disabled={!consent || samples.length !== 3 || busy} onClick={enroll}
            className="min-h-11 px-4 rounded-lg border border-deck-accent text-sm disabled:opacity-40">
            {busy ? '등록 중…' : '목소리 등록'}
          </button>
        </>
      )}
      {recorder.error && <p className="text-xs text-red-400" role="alert">{recorder.error}</p>}
      {message && <p className="text-xs" role="status">{message}</p>}
    </section>
  );
}
