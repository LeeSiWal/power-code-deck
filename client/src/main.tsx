import React from 'react';
import ReactDOM from 'react-dom/client';
import App from './App';
import { initUiScale } from './lib/uiScale';
import './styles/scroll.css';
import './styles/globals.css';
import './styles/animations.css';
import './styles/notifications.css';

// Scale the whole UI to the viewport (bigger display → bigger UI) before first paint,
// so the app renders at the right size immediately instead of flashing at 16px.
initUiScale();

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);

// Register service worker for PWA
if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker
      .register('/sw.js')
      // Heal an existing push subscription so it carries this device's id — otherwise
      // device-targeted notifications can't be aimed at devices that subscribed before
      // the feature existed (they'd get nothing until a manual re-toggle).
      .then(() => import('./lib/push').then((m) => m.healPush()))
      .catch(() => {});
  });
}

// Mobile debug console — ?debug query param activates
if (new URLSearchParams(location.search).has('debug')) {
  import('eruda').then((mod) => {
    mod.default.init();
  }).catch(() => {});
}
