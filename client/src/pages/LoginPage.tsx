import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { PinInput } from '../components/auth/PinInput';
import { useAuth } from '../hooks/useAuth';
import { useAppStore } from '../stores/appStore';

export function LoginPage() {
  const { isAuthenticated, login } = useAuth();
  const { authConfig } = useAppStore();
  const navigate = useNavigate();

  useEffect(() => {
    if (isAuthenticated) navigate('/', { replace: true });
  }, [isAuthenticated, navigate]);

  const appName = authConfig?.appName || 'PowerCodeDeck';
  const method = authConfig?.authMethod ?? 'pin';

  return (
    <div className="flex flex-col items-center justify-center h-[100dvh] safe-top px-4 bg-deck-bg">
      <h1 className="text-2xl font-bold mb-2">{appName}</h1>
      <p className="text-sm text-deck-text-dim mb-8">
        {method === 'password' ? 'Enter password to continue' : 'Enter PIN to continue'}
      </p>
      {method === 'password' ? (
        <PasswordForm onSubmit={login} />
      ) : (
        <PinInput onSubmit={login} />
      )}
    </div>
  );
}

function PasswordForm({ onSubmit }: { onSubmit: (secret: string) => Promise<void> }) {
  const [value, setValue] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!value || loading) return;
    setLoading(true);
    setError(null);
    try {
      await onSubmit(value);
    } catch (err: any) {
      setValue('');
      setError(err.message || 'Invalid password');
    } finally {
      setLoading(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex flex-col items-center w-full max-w-[280px]">
      <input
        type="password"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        autoFocus
        autoComplete="current-password"
        placeholder="Password"
        className="w-full px-3 py-2.5 rounded-lg text-base outline-none bg-deck-surface text-deck-text border border-deck-border focus:border-deck-accent"
      />
      {error && <p className="text-sm mt-3 text-deck-danger">{error}</p>}
      <button
        type="submit"
        disabled={loading || !value}
        className="btn-primary w-full mt-4 px-4 py-2.5 rounded-lg text-sm font-medium disabled:opacity-40"
      >
        {loading ? 'Verifying...' : 'Continue'}
      </button>
    </form>
  );
}
