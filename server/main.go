package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"powercodedeck/auth"
	"powercodedeck/cli"
	"powercodedeck/config"
	"powercodedeck/db"
	"powercodedeck/handlers"
	"powercodedeck/middleware"
	"powercodedeck/services"
	"powercodedeck/version"
	"powercodedeck/ws"

	"github.com/gorilla/mux"
)

//go:embed all:static
var staticFiles embed.FS

func main() {
	if cli.IsSubcommand(os.Args) {
		cli.Run(os.Args[1:])
		return
	}
	cfg := config.Load()

	// LAN handoff without an explicit LAN_URL: adopt the auto-detected private IP
	// as the LAN origin BEFORE anything reads AllowedOrigins/AllowedHosts, so the
	// two guards stay in sync. Otherwise the DNS-rebinding Host guard (which does
	// auto-detect the IP) lets the page load, but the Origin allow-list (which did
	// NOT) rejects the anonymous-token mint and the WebSocket handshake — leaving a
	// LAN device (iPad/phone) stuck on "Connecting…" with no token. Never overrides
	// an explicit LAN_URL.
	if cfg.LanHandoffEnabled && cfg.LanURL == "" {
		if ip := services.DetectLANIP(); ip != "" {
			cfg.LanURL = "http://" + ip + ":" + cfg.Port
		}
	}

	database := db.Init(cfg.DBPath)

	// Services
	// PowerCodeDeck owns its PTY sessions directly via the internal engine — no
	// tmux. handlers/hub/agent only ever talk to the SessionEngine interface.
	if cfg.SessionEngine != "" && cfg.SessionEngine != "internal" {
		log.Printf("Warning: POWERCODEDECK_SESSION_ENGINE=%s is no longer supported; "+
			"PowerCodeDeck now always uses the internal PTY session engine.", cfg.SessionEngine)
	}
	var sessionEngine services.SessionEngine = services.NewInternalPtySessionEngine(cfg.ScrollbackBytes)
	log.Printf("Session engine: internal")
	agentSvc := services.NewAgentService(database, sessionEngine)
	// Transcript-based agent/sub-agent activity watcher (replaces client-side
	// terminal scraping). Wired to the hub below once it exists.
	activitySvc := services.NewActivityManager()
	agentSvc.SetActivityManager(activitySvc)
	fileSvc := services.NewFileService()
	watcherSvc := services.NewWatcherService()
	projectSvc := services.NewProjectService(database)
	projectSvc.SetWorkspaceRoot(cfg.WorkspaceRoot) // default project-browser root
	authSvc := auth.NewAuthService(cfg.AuthEnabled, cfg.AuthMethod, cfg.Pin, cfg.PasswordHash, cfg.JWTSecret)
	gitSvc := services.NewGitService()
	portScanner := services.NewPortScanner()
	notifSvc := services.NewNotificationService(database)
	handoffSvc := services.NewHandoffService(database)
	handoffSvc.CleanupExpired()

	// Web Push (VAPID). The "sub" claim is contact info for the push services; prefer
	// an explicit env, else the deck's own https origin, else a syntactically-valid
	// mailto fallback. Push services (incl. Apple's) only check it's a well-formed
	// mailto/https, not that it's reachable.
	vapidContact := firstNonEmpty(
		os.Getenv("POWERCODEDECK_VAPID_CONTACT"),
		os.Getenv("AGENTDECK_VAPID_CONTACT"),
		firstHTTPSOrigin(cfg.AllowedOrigins()),
		"mailto:webpush@localhost",
	)
	pushSvc := services.NewPushService(database, vapidContact)

	// WebSocket hub. The allow-list rejects cross-origin handshakes (drive-by RCE).
	hub := ws.NewHub(sessionEngine, watcherSvc, agentSvc, gitSvc, portScanner, notifSvc, cfg.AllowedOrigins())
	hub.SetPushService(pushSvc) // wired before SetNativeService so triggers can push
	go hub.Run()

	// Native track: Claude driven as a structured stream (no PTY). The base URL is
	// where the permission bridge calls back — always loopback, never cfg.BindHost:
	// the bridge is our own child process on this machine, and the endpoint should
	// not be reachable from the LAN even when the deck itself is.
	nativeSvc := services.NewNativeService("http://127.0.0.1:" + cfg.Port)
	// Remember Claude's own conversation id per agent so reopening (or a server
	// restart) continues the conversation instead of starting a blank one.
	nativeSvc.SetPersistence(agentSvc.SetClaudeSessionID, agentSvc.ClaudeSessionID)
	nativeSvc.SetConfigPersistence(agentSvc.SetNativeConfig, agentSvc.NativeConfig)
	hub.SetNativeService(nativeSvc)

	// Control Room (v0.3.0): the multi-session overview aggregator. It projects
	// existing state (agents + activity + pending approvals + notifications) into
	// per-session summaries and pushes changed ones as coalesced agent:summaries
	// batches. Deliberately additive — the existing dashboard/REST list is untouched.
	controlRoom := services.NewControlRoomService(agentSvc, nativeSvc.Broker(), notifSvc, services.AttentionThresholds{
		IdleMs:         int64(cfg.ControlRoomIdleMinutes) * 60 * 1000,
		StartupGraceMs: int64(cfg.ControlRoomStartupGraceSeconds) * 1000,
	})
	controlRoom.SetEmitter(func(sums []services.AgentSummary) {
		hub.BroadcastAll(ws.EventAgentSummaries, ws.AgentSummariesPayload{Summaries: sums})
	})
	hub.SetControlRoom(controlRoom)
	go controlRoom.Run(
		time.Duration(cfg.ControlRoomSummaryBatchMs)*time.Millisecond,
		time.Duration(cfg.ControlRoomSnapshotCorrectionSec)*time.Second,
	)

	// Session output → broadcast to every viewer of that session.
	sessionEngine.SetOutputHandler(func(sessionID string, data []byte) {
		hub.BroadcastToAgent(sessionID, ws.EventTerminalOutput, ws.TerminalOutputPayload{
			AgentID: sessionID,
			Data:    string(data),
		})
	})

	// Structured agent/sub-agent activity snapshots → broadcast to that session's
	// viewers, and tee into the Control Room so a tile's last tool/target and the
	// stalled test stay current without the overview watching each session.
	activitySvc.SetEmitter(func(agentID string, snap services.AgentActivitySnapshot) {
		hub.BroadcastToAgent(agentID, ws.EventAgentActivity, snap)
		controlRoom.OnActivity(agentID, snap)
	})

	// Router
	r := mux.NewRouter()

	// Global middleware. HostCheck runs first to block DNS-rebinding before any
	// handler sees the request. The auto-detected LAN IP is already folded into
	// cfg.LanURL above, so AllowedHosts (and AllowedOrigins) both include it.
	allowedHosts := cfg.AllowedHosts()
	r.Use(middleware.Helmet)
	r.Use(middleware.HostCheck(allowedHosts))
	r.Use(middleware.CORS(cfg.CORSOrigins))

	// Rate limiter for auth endpoints
	authLimiter := middleware.NewRateLimiter(10, time.Minute)

	// Health check (no auth required). Exposes version + auth config so the
	// client can skip the login page in no-auth mode.
	healthHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w,
			`{"status":"ok","appName":%q,"version":%q,"authEnabled":%t,"authMethod":%q,"handoffEnabled":%t}`,
			version.AppName, version.Version, cfg.AuthEnabled, cfg.AuthMethod, cfg.HandoffEnabled,
		)
	}
	// The permission bridge (`pcd mcp-approve`, spawned by Claude) asks here whether
	// a tool may run, and this call BLOCKS until a human answers on some device.
	// Deliberately outside /api and its auth middleware: the caller is our own
	// child process holding a per-session token, not a browser with a JWT.
	r.HandleFunc("/internal/native/approve", handlers.NativeApprove(nativeSvc.Broker(), nativeSvc.Tokens())).Methods("POST")

	r.HandleFunc("/api/auth/health", healthHandler).Methods("GET")
	r.HandleFunc("/api/health", healthHandler).Methods("GET")

	// Auth endpoints (no JWT required)
	authRouter := r.PathPrefix("/api/auth").Subrouter()
	authRouter.Use(authLimiter.Middleware)
	authRouter.HandleFunc("/login", handlers.Login(authSvc)).Methods("POST", "OPTIONS")
	authRouter.HandleFunc("/refresh", handlers.Refresh(authSvc)).Methods("POST", "OPTIONS")
	// Anonymous token for no-auth mode so the WebSocket can always require a token.
	authRouter.HandleFunc("/anonymous", handlers.AnonymousToken(authSvc, cfg)).Methods("POST", "OPTIONS")
	// Handoff exchange: trade a session-scoped handoff cookie for normal tokens.
	authRouter.HandleFunc("/handoff/exchange", handlers.HandoffExchange(authSvc)).Methods("POST", "OPTIONS")

	// Protected API endpoints
	api := r.PathPrefix("/api").Subrouter()
	api.Use(auth.Middleware(authSvc))

	// Agents
	api.HandleFunc("/agents/slash-commands", handlers.SlashCommands(agentSvc)).Methods("GET")
	api.HandleFunc("/agents", handlers.ListAgents(agentSvc)).Methods("GET")
	api.HandleFunc("/agents", handlers.CreateAgent(agentSvc, hub)).Methods("POST")
	api.HandleFunc("/agents/{id}", handlers.GetAgent(agentSvc)).Methods("GET")
	api.HandleFunc("/agents/{id}", handlers.DeleteAgent(agentSvc, hub)).Methods("DELETE")
	api.HandleFunc("/agents/{id}/restart", handlers.RestartAgent(agentSvc, hub)).Methods("POST")

	// Past-session history (Claude Code transcripts for the agent's project).
	api.HandleFunc("/agents/{id}/sessions/new", handlers.NewSession(agentSvc, hub)).Methods("POST")
	api.HandleFunc("/agents/{id}/sessions", handlers.ListSessions(agentSvc)).Methods("GET")
	api.HandleFunc("/agents/{id}/sessions/{sid}", handlers.GetSession(agentSvc)).Methods("GET")
	api.HandleFunc("/agents/{id}/sessions/{sid}", handlers.DeleteSession(agentSvc)).Methods("DELETE")
	api.HandleFunc("/agents/{id}/sessions/{sid}/resume", handlers.ResumeSession(agentSvc, hub)).Methods("POST")

	// Files
	api.HandleFunc("/files/tree", handlers.FileTree(fileSvc, agentSvc, projectSvc, cfg)).Methods("GET")
	api.HandleFunc("/files/read", handlers.ReadFile(fileSvc, agentSvc, projectSvc, cfg)).Methods("GET")
	api.HandleFunc("/files/raw", handlers.RawFile(fileSvc, agentSvc, projectSvc, cfg)).Methods("GET")
	api.HandleFunc("/files/write", handlers.WriteFile(fileSvc, agentSvc, projectSvc, cfg)).Methods("PUT")
	api.HandleFunc("/files/mkdir", handlers.Mkdir(fileSvc, agentSvc, projectSvc, cfg)).Methods("POST")
	api.HandleFunc("/files/delete", handlers.DeleteFile(fileSvc, agentSvc, projectSvc, cfg)).Methods("DELETE")
	api.HandleFunc("/files/rename", handlers.RenameFile(fileSvc, agentSvc, projectSvc, cfg)).Methods("PATCH")
	api.HandleFunc("/files/stat", handlers.FileStat(fileSvc, agentSvc, projectSvc, cfg)).Methods("GET")
	api.HandleFunc("/agents/{id}/attach", handlers.AttachFile(fileSvc, agentSvc, projectSvc, cfg)).Methods("POST")

	// Projects
	api.HandleFunc("/projects/recent", handlers.RecentProjects(projectSvc)).Methods("GET")
	api.HandleFunc("/projects/recent/{id}", handlers.DeleteRecentProject(projectSvc)).Methods("DELETE")
	api.HandleFunc("/projects/browse", handlers.BrowseDir(projectSvc)).Methods("GET")
	api.HandleFunc("/projects/detect", handlers.DetectProject(projectSvc)).Methods("GET")
	api.HandleFunc("/projects/search", handlers.SearchProjects(projectSvc)).Methods("GET")
	api.HandleFunc("/projects/create", handlers.CreateProject(projectSvc)).Methods("POST")
	api.HandleFunc("/projects/delete", handlers.DeleteProject(projectSvc, agentSvc)).Methods("DELETE")
	api.HandleFunc("/projects/rename", handlers.RenameProject(projectSvc, agentSvc)).Methods("PATCH")

	// Logs
	api.HandleFunc("/logs", handlers.SearchLogs(database)).Methods("GET")
	api.HandleFunc("/logs/{agentId}", handlers.AgentLogs(database)).Methods("GET")

	// Notifications
	api.HandleFunc("/notifications", handlers.ListNotifications(notifSvc)).Methods("GET")
	api.HandleFunc("/notifications/clear", handlers.ClearNotifications(notifSvc)).Methods("POST")

	// Control Room (v0.3.0) — initial snapshot of the overview + global approval queue.
	// Live deltas arrive over the WebSocket (agent:summaries, approval:resolved).
	api.HandleFunc("/control/summaries", handlers.ControlSummaries(controlRoom)).Methods("GET")
	api.HandleFunc("/approvals", handlers.ListApprovals(nativeSvc)).Methods("GET")

	// Web Push — VAPID public key + subscription lifecycle.
	api.HandleFunc("/push/vapid", handlers.PushVAPIDKey(pushSvc)).Methods("GET")
	api.HandleFunc("/push/subscribe", handlers.PushSubscribe(pushSvc)).Methods("POST")
	api.HandleFunc("/push/unsubscribe", handlers.PushUnsubscribe(pushSvc)).Methods("POST")

	// Proxy for external URLs (iframe X-Frame-Options bypass)
	api.HandleFunc("/proxy", handlers.ProxyHandler()).Methods("GET")

	// Agent meta + send
	api.HandleFunc("/agents/{id}/send", handlers.SendToAgent(agentSvc, hub)).Methods("POST")
	api.HandleFunc("/agents/{id}/meta", handlers.GetAgentMeta(gitSvc, portScanner, notifSvc)).Methods("GET")
	api.HandleFunc("/agents/{id}/meta/status", handlers.SetAgentMetaStatus(hub)).Methods("POST")
	api.HandleFunc("/agents/{id}/meta/progress", handlers.SetAgentMetaProgress(hub)).Methods("POST")
	api.HandleFunc("/agents/{id}/meta/log", handlers.AddAgentMetaLog(hub)).Methods("POST")
	api.HandleFunc("/agents/{id}/notifications/read", handlers.MarkAgentNotificationsRead(notifSvc)).Methods("POST")

	// Session Handoff — issue a one-time "Continue on Mobile" token/QR for a session.
	api.HandleFunc("/agents/{id}/handoff", handlers.CreateHandoff(handoffSvc, agentSvc, cfg)).Methods("POST")

	// Session Handoff redeem — scanned from a QR. No bearer auth: the one-time
	// token itself is the credential. Registered before the SPA/static catch-all.
	r.HandleFunc("/handoff/{token}", handlers.RedeemHandoff(handoffSvc, agentSvc, authSvc, cfg)).Methods("GET")

	// WebSocket. Always requires a valid access token — even in no-auth mode,
	// where the local browser first mints an anonymous token from
	// /api/auth/anonymous. This (with the Origin check in the hub) closes the
	// drive-by hole where any web page could open ws://localhost/ws and inject
	// terminal input.
	r.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if err := authSvc.VerifyAccessToken(token); err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		hub.HandleWebSocket(w, r)
	})

	// Static files (embedded Vite build)
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Printf("No embedded static files found, serving API only")
	} else {
		// SPA frontend routes — serve index.html for client-side routing
		spaRoutes := []string{"/agents", "/dashboard", "/control", "/login", "/settings", "/logs", "/launch"}
		for _, route := range spaRoutes {
			route := route
			r.PathPrefix(route).HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if f, err := staticFS.(fs.ReadFileFS).ReadFile("index.html"); err == nil {
					w.Header().Set("Content-Type", "text/html")
					w.Header().Set("Cache-Control", "no-cache")
					w.Write(f)
				}
			})
		}

		fileServer := http.FileServer(http.FS(staticFS))

		r.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if path == "/" {
				path = "/index.html"
			}

			// Try to serve the file
			if f, err := staticFS.(fs.ReadFileFS).ReadFile(path[1:]); err == nil {
				switch {
				case len(path) > 3 && path[len(path)-3:] == ".js":
					w.Header().Set("Content-Type", "application/javascript")
				case len(path) > 4 && path[len(path)-4:] == ".css":
					w.Header().Set("Content-Type", "text/css")
				case len(path) > 5 && path[len(path)-5:] == ".html":
					w.Header().Set("Content-Type", "text/html")
				case len(path) > 5 && path[len(path)-5:] == ".json":
					w.Header().Set("Content-Type", "application/json")
				case len(path) > 4 && path[len(path)-4:] == ".svg":
					w.Header().Set("Content-Type", "image/svg+xml")
				case len(path) > 4 && path[len(path)-4:] == ".png":
					w.Header().Set("Content-Type", "image/png")
				}
				// Hashed /assets/* are immutable; always revalidate the app shell
				// (index.html, sw.js, manifest) so a new build's asset hashes are
				// picked up instead of a stale cached shell (iOS Safari PWA caching).
				if len(path) >= 8 && path[:8] == "/assets/" {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				w.Write(f)
				return
			}

			// SPA fallback
			w.Header().Set("Cache-Control", "no-cache")
			r.URL.Path = "/index.html"
			fileServer.ServeHTTP(w, r)
		})
	}

	url := fmt.Sprintf("http://localhost:%s", cfg.Port)

	// Start server in goroutine. BindHost defaults to 127.0.0.1 (localhost only);
	// set POWERCODEDECK_BIND_HOST=0.0.0.0 to expose it on the LAN for handoff.
	srv := &http.Server{
		Addr:    cfg.BindHost + ":" + cfg.Port,
		Handler: r,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	// Wait for server to be ready
	waitForServer(url, 3*time.Second)

	// Print friendly startup message
	printBanner(cfg, url)

	// Auto-open browser
	openBrowser(url)

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println()
	log.Printf("Shutting down %s...", version.AppName)

	// Stop accepting new connections and let in-flight requests finish (up to 5s).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown timed out: %v", err)
	}

	// Active PTY sessions are intentionally kept running here (Detach ≠ Kill).
	// Their lifetime is tied to this process, so they end when it exits; we do
	// not force-kill them on Ctrl+C.
	log.Printf("HTTP server stopped; active sessions end with this process.")

	database.Close()
	log.Println("Goodbye!")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// firstHTTPSOrigin returns the first https origin from the allow-list, used as the
// VAPID contact when no explicit one is set — a real origin is the friendliest
// "sub" a push service can be handed.
func firstHTTPSOrigin(origins []string) string {
	for _, o := range origins {
		if len(o) >= 8 && o[:8] == "https://" {
			return o
		}
	}
	return ""
}

func waitForServer(url string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/api/auth/health")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		// Check if running inside WSL → use Windows browser
		if isWSL() {
			cmd = exec.Command("cmd.exe", "/c", "start", url)
		} else {
			cmd = exec.Command("xdg-open", url)
		}
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	}
	if cmd != nil {
		cmd.Start()
	}
}

func isWSL() bool {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	lower := string(data)
	return len(lower) > 0 && (contains(lower, "microsoft") || contains(lower, "WSL"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			c := s[i+j]
			d := sub[j]
			// case-insensitive
			if c != d && c != d+32 && c != d-32 {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func printBanner(cfg *config.Config, url string) {
	authLabel := "disabled"
	if cfg.AuthEnabled {
		authLabel = cfg.AuthMethod // "pin" or "password" — never print the secret
	}

	fmt.Println()
	fmt.Println("  ================================================")
	fmt.Printf("     %s v%s\n", version.AppName, version.Version)
	fmt.Printf("     %s\n", version.Tagline)
	fmt.Println("  ================================================")
	fmt.Println()
	fmt.Printf("     URL  : %s\n", url)
	fmt.Printf("     Auth : %s\n", authLabel)
	fmt.Println()
	if !cfg.AuthEnabled {
		fmt.Println("  Warning:")
		fmt.Printf("  %s authentication is disabled.\n", version.AppName)
		fmt.Println("  Do not expose this service directly to the public internet.")
		fmt.Println("  Use Caddy + Authelia, Tailscale, VPN, or SSH tunnel.")
		fmt.Println()
	}
	if !cfg.AuthEnabled && cfg.LanHandoffEnabled {
		fmt.Println("  Warning (LAN handoff):")
		fmt.Println("  Authentication is disabled and LAN handoff is enabled.")
		fmt.Println("  Anyone on the same network who can reach this URL may open handoff links.")
		fmt.Println("  Enable PIN/password auth or keep the service behind VPN/Tailscale.")
		fmt.Println()
	}
	fmt.Println("     Browser will open automatically.")
	fmt.Println("     Press Ctrl+C to stop the server.")
	fmt.Println()
	fmt.Println("  ================================================")
	fmt.Println()
}
