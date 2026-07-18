import { useState, useCallback, useEffect, useRef } from 'react';

const PIN_LENGTH = 6;

interface PinInputProps {
  onSubmit: (pin: string) => Promise<void>;
  error?: string | null;
}

export function PinInput({ onSubmit, error: externalError }: PinInputProps) {
  const [digits, setDigits] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [lockout, setLockout] = useState(0);

  useEffect(() => {
    if (externalError) setError(externalError);
  }, [externalError]);

  useEffect(() => {
    if (lockout <= 0) return;
    const timer = setInterval(() => {
      setLockout((v) => (v <= 1 ? 0 : v - 1));
    }, 1000);
    return () => clearInterval(timer);
  }, [lockout]);

  const submitPin = useCallback(async (pin: string) => {
    setLoading(true);
    setError(null);
    try {
      await onSubmit(pin);
    } catch (err: any) {
      setDigits([]);
      setError(err.message || 'Invalid PIN');
      if (navigator.vibrate) navigator.vibrate(200);
    } finally {
      setLoading(false);
    }
  }, [onSubmit]);

  const handleDigit = useCallback((digit: string) => {
    if (loading || lockout > 0) return;
    setError(null);
    setDigits((prev) => {
      const next = [...prev, digit];
      if (next.length === PIN_LENGTH) {
        setTimeout(() => submitPin(next.join('')), 0);
      }
      return next.length <= PIN_LENGTH ? next : prev;
    });
  }, [loading, lockout, submitPin]);

  const handleBackspace = useCallback(() => {
    if (loading) return;
    setDigits((prev) => prev.slice(0, -1));
    setError(null);
  }, [loading]);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key >= '0' && e.key <= '9') handleDigit(e.key);
      else if (e.key === 'Backspace') handleBackspace();
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [handleDigit, handleBackspace]);

  return (
    <div className="flex flex-col items-center">
      {/* PIN dots */}
      <div className="flex gap-3 mb-8">
        {Array.from({ length: PIN_LENGTH }).map((_, i) => (
          <div
            key={i}
            className="w-4 h-4 rounded-full border-2 transition-all"
            style={{
              borderColor: error && digits.length === 0
                ? '#ef4444'
                : i < digits.length ? '#6366f1' : '#26262f',
              background: i < digits.length ? '#6366f1' : 'transparent',
              transform: i < digits.length ? 'scale(1.1)' : 'scale(1)',
            }}
          />
        ))}
      </div>

      {error && <p className="text-sm mb-4 text-deck-danger">{error}</p>}
      {lockout > 0 && <p className="text-sm mb-4 text-deck-warning">Try again in {lockout}s</p>}

      {/* Number pad */}
      <div className="grid grid-cols-3 gap-3 max-w-[280px]">
        {['1','2','3','4','5','6','7','8','9'].map((d) => (
          <button
            key={d}
            onClick={() => handleDigit(d)}
            disabled={loading || lockout > 0}
            className="w-20 h-14 rounded-lg text-xl font-medium transition-colors disabled:opacity-40 touch-manipulation select-none bg-deck-surface text-deck-text hover:bg-deck-border"
          >
            {d}
          </button>
        ))}
        <div />
        <button
          onClick={() => handleDigit('0')}
          disabled={loading || lockout > 0}
          className="w-20 h-14 rounded-lg text-xl font-medium transition-colors disabled:opacity-40 touch-manipulation select-none bg-deck-surface text-deck-text hover:bg-deck-border"
        >
          0
        </button>
        <button
          onClick={handleBackspace}
          disabled={loading}
          className="w-20 h-14 rounded-lg text-lg transition-colors disabled:opacity-40 touch-manipulation select-none bg-deck-surface text-deck-text-dim hover:bg-deck-border"
        >
          &#x232B;
        </button>
      </div>

      {loading && <div className="mt-6 text-sm text-deck-text-dim">Verifying...</div>}
    </div>
  );
}
