import { useCallback } from 'react';
import { useNavigate } from 'react-router-dom';

/**
 * Back navigation that goes to the ACTUAL previous page (browser-style), instead of
 * always jumping to a fixed destination. Falls back to a default route when there's
 * no in-app history to pop — a fresh load, a deep link, or the first page of the
 * session — so the button is never a dead end that would exit the app.
 *
 * React Router stamps each history entry with an incrementing `idx`; idx > 0 means
 * there's somewhere to go back to within the app.
 */
export function useGoBack(fallback = '/dashboard') {
  const navigate = useNavigate();
  return useCallback(() => {
    const idx = (window.history.state as { idx?: number } | null)?.idx ?? 0;
    if (idx > 0) navigate(-1);
    else navigate(fallback);
  }, [navigate, fallback]);
}
