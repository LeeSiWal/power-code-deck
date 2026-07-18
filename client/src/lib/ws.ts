type EventHandler = (payload: any) => void;

class AgentDeckWS {
  private ws: WebSocket | null = null;
  private listeners = new Map<string, Set<EventHandler>>();
  private reconnectTimer: number | null = null;
  private livenessTimer: number | null = null;
  private token: string | null = null;
  // Messages sent while the socket wasn't OPEN (still connecting, or between a
  // drop and the reconnect). Without this they were silently dropped — so a
  // native:open / terminal:attach fired at mount, before the socket finished
  // connecting, vanished, and the session only recovered on a manual refresh.
  // Flushed in order the instant the socket opens. Bounded so a long outage can't
  // grow it without limit.
  private sendQueue: string[] = [];

  constructor() {
    // Mobile Safari (iPad/iPhone) freezes background tabs and silently drops the
    // socket. When the app returns to the foreground, reconnect right away rather
    // than waiting for the (also frozen) 3s retry timer or the server's 60s ping
    // timeout — so the user never has to hit refresh.
    if (typeof window !== 'undefined') {
      const wake = () => this.wake();
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') wake();
      });
      window.addEventListener('pageshow', wake);
      window.addEventListener('focus', wake);
      window.addEventListener('online', wake);
    }
  }

  connect(token: string) {
    if (
      this.token === token &&
      (this.ws?.readyState === WebSocket.OPEN || this.ws?.readyState === WebSocket.CONNECTING)
    ) {
      return;
    }

    this.token = token;
    this.cleanup();

    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    this.ws = new WebSocket(`${protocol}//${location.host}/ws?token=${token}`);

    this.ws.onopen = () => {
      console.log('[WS] Connected');
      // Flush anything queued while we were connecting/reconnecting FIRST, so a
      // native:open / terminal:attach that fired before the socket was ready
      // actually reaches the server instead of being lost.
      const queued = this.sendQueue;
      this.sendQueue = [];
      for (const m of queued) this.ws?.send(m);
      // Then emit 'open' so components can re-attach.
      this.listeners.get('open')?.forEach((fn) => fn({}));
    };

    this.ws.onmessage = (e) => {
      try {
        const { event, payload } = JSON.parse(e.data);
        // Reply to our wake-time liveness probe (see wake/probeLiveness).
        if (event === 'pong') {
          this.clearLiveness();
          return;
        }
        this.listeners.get(event)?.forEach((fn) => fn(payload));
      } catch (err) {
        console.error('[WS] Parse error:', err);
      }
    };

    this.ws.onclose = () => {
      console.log('[WS] Disconnected, reconnecting in 3s...');
      this.clearLiveness();
      // Emit 'close' to all listeners
      this.listeners.get('close')?.forEach((fn) => fn({}));
      this.reconnectTimer = window.setTimeout(() => {
        if (this.token) this.connect(this.token);
      }, 3000);
    };

    this.ws.onerror = (err) => {
      console.error('[WS] Error:', err);
    };
  }

  /**
   * Called when the page returns to the foreground or the network comes back.
   * If the socket is already closed, reconnect immediately; if it still claims
   * to be OPEN, verify it is actually alive — iOS commonly leaves a dead
   * "zombie" socket stuck in the OPEN state after a suspend/resume.
   */
  private wake() {
    if (!this.token) return;
    const state = this.ws?.readyState;
    if (state === WebSocket.OPEN) {
      this.probeLiveness();
    } else if (state !== WebSocket.CONNECTING) {
      this.reconnectNow();
    }
  }

  /** Ping the server and force a reconnect if no pong arrives shortly. */
  private probeLiveness() {
    if (this.livenessTimer) return; // a probe is already in flight
    this.livenessTimer = window.setTimeout(() => {
      this.livenessTimer = null;
      console.log('[WS] Liveness probe timed out, forcing reconnect');
      this.reconnectNow();
    }, 3000);
    try {
      this.ws?.send(JSON.stringify({ event: 'ping', payload: {} }));
    } catch {
      this.reconnectNow(); // send threw → socket is dead; clears the timer via cleanup
    }
  }

  private clearLiveness() {
    if (this.livenessTimer) {
      clearTimeout(this.livenessTimer);
      this.livenessTimer = null;
    }
  }

  /** Drop the current (dead) socket and open a fresh one right now. */
  private reconnectNow() {
    if (!this.token) return;
    const token = this.token;
    this.cleanup();
    this.connect(token);
  }

  send(event: string, payload: any) {
    const msg = JSON.stringify({ event, payload });
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(msg);
    } else {
      this.sendQueue.push(msg);
      if (this.sendQueue.length > 200) this.sendQueue.shift();
    }
  }

  on(event: string, fn: EventHandler): () => void {
    if (!this.listeners.has(event)) {
      this.listeners.set(event, new Set());
    }
    this.listeners.get(event)!.add(fn);
    return () => {
      this.listeners.get(event)?.delete(fn);
    };
  }

  off(event: string, fn: EventHandler) {
    this.listeners.get(event)?.delete(fn);
  }

  get connected() {
    return this.ws?.readyState === WebSocket.OPEN;
  }

  private cleanup() {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.clearLiveness();
    if (this.ws) {
      this.ws.onclose = null;
      this.ws.close();
      this.ws = null;
    }
  }

  disconnect() {
    this.token = null;
    this.cleanup();
  }
}

export const agentDeckWS = new AgentDeckWS();
