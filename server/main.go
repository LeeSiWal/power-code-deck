package main

import (
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
	database := db.Init(cfg.DBPath)

	// Services
	tmuxSvc := services.NewTmuxService()
	ptySvc := services.NewPtyService()
	agentSvc := services.NewAgentService(database, tmuxSvc, ptySvc)
	fileSvc := services.NewFileService()
	watcherSvc := services.NewWatcherService()
	projectSvc := services.NewProjectService(database)
	authSvc := auth.NewAuthService(cfg.AuthEnabled, cfg.AuthMethod, cfg.Pin, cfg.PasswordHash, cfg.JWTSecret)
	gitSvc := services.NewGitService()
	portScanner := services.NewPortScanner()
	notifSvc := services.NewNotificationService(database)

	// WebSocket hub
	hub := ws.NewHub(ptySvc, watcherSvc, agentSvc, gitSvc, portScanner, notifSvc)
	go hub.Run()

	// Router
	r := mux.NewRouter()

	// Global middleware
	r.Use(middleware.Helmet)
	r.Use(middleware.CORS(cfg.CORSOrigins))

	// Rate limiter for auth endpoints
	authLimiter := middleware.NewRateLimiter(10, time.Minute)

	// Health check (no auth required). Exposes version + auth config so the
	// client can skip the login page in no-auth mode.
	healthHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w,
			`{"status":"ok","appName":%q,"version":%q,"authEnabled":%t,"authMethod":%q}`,
			version.AppName, version.Version, cfg.AuthEnabled, cfg.AuthMethod,
		)
	}
	r.HandleFunc("/api/auth/health", healthHandler).Methods("GET")
	r.HandleFunc("/api/health", healthHandler).Methods("GET")

	// Auth endpoints (no JWT required)
	authRouter := r.PathPrefix("/api/auth").Subrouter()
	authRouter.Use(authLimiter.Middleware)
	authRouter.HandleFunc("/login", handlers.Login(authSvc)).Methods("POST", "OPTIONS")
	authRouter.HandleFunc("/refresh", handlers.Refresh(authSvc)).Methods("POST", "OPTIONS")

	// Protected API endpoints
	api := r.PathPrefix("/api").Subrouter()
	api.Use(auth.Middleware(authSvc))

	// Agents
	api.HandleFunc("/agents/slash-commands", handlers.SlashCommands()).Methods("GET")
	api.HandleFunc("/agents", handlers.ListAgents(agentSvc)).Methods("GET")
	api.HandleFunc("/agents", handlers.CreateAgent(agentSvc, hub)).Methods("POST")
	api.HandleFunc("/agents/{id}", handlers.GetAgent(agentSvc)).Methods("GET")
	api.HandleFunc("/agents/{id}", handlers.DeleteAgent(agentSvc, hub)).Methods("DELETE")
	api.HandleFunc("/agents/{id}/restart", handlers.RestartAgent(agentSvc, hub)).Methods("POST")

	// Files
	api.HandleFunc("/files/tree", handlers.FileTree(fileSvc, agentSvc)).Methods("GET")
	api.HandleFunc("/files/read", handlers.ReadFile(fileSvc, agentSvc)).Methods("GET")
	api.HandleFunc("/files/write", handlers.WriteFile(fileSvc, agentSvc)).Methods("PUT")
	api.HandleFunc("/files/mkdir", handlers.Mkdir(fileSvc, agentSvc)).Methods("POST")
	api.HandleFunc("/files/delete", handlers.DeleteFile(fileSvc, agentSvc)).Methods("DELETE")
	api.HandleFunc("/files/rename", handlers.RenameFile(fileSvc, agentSvc)).Methods("PATCH")
	api.HandleFunc("/files/stat", handlers.FileStat(fileSvc, agentSvc)).Methods("GET")

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

	// Proxy for external URLs (iframe X-Frame-Options bypass)
	api.HandleFunc("/proxy", handlers.ProxyHandler()).Methods("GET")

	// Agent meta + send
	api.HandleFunc("/agents/{id}/send", handlers.SendToAgent(agentSvc, hub)).Methods("POST")
	api.HandleFunc("/agents/{id}/meta", handlers.GetAgentMeta(gitSvc, portScanner, notifSvc)).Methods("GET")
	api.HandleFunc("/agents/{id}/meta/status", handlers.SetAgentMetaStatus(hub)).Methods("POST")
	api.HandleFunc("/agents/{id}/meta/progress", handlers.SetAgentMetaProgress(hub)).Methods("POST")
	api.HandleFunc("/agents/{id}/meta/log", handlers.AddAgentMetaLog(hub)).Methods("POST")
	api.HandleFunc("/agents/{id}/notifications/read", handlers.MarkAgentNotificationsRead(notifSvc)).Methods("POST")

	// WebSocket (auth via query param; skipped in no-auth mode)
	r.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if authSvc.Enabled() {
			token := r.URL.Query().Get("token")
			if err := authSvc.VerifyToken(token); err != nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		hub.HandleWebSocket(w, r)
	})

	// Static files (embedded Vite build)
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Printf("No embedded static files found, serving API only")
	} else {
		// SPA frontend routes — serve index.html for client-side routing
		spaRoutes := []string{"/agents", "/dashboard", "/login", "/settings", "/logs", "/launch"}
		for _, route := range spaRoutes {
			route := route
			r.PathPrefix(route).HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if f, err := staticFS.(fs.ReadFileFS).ReadFile("index.html"); err == nil {
					w.Header().Set("Content-Type", "text/html")
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
				w.Write(f)
				return
			}

			// SPA fallback
			r.URL.Path = "/index.html"
			fileServer.ServeHTTP(w, r)
		})
	}

	url := fmt.Sprintf("http://localhost:%s", cfg.Port)

	// Start server in goroutine
	go func() {
		if err := http.ListenAndServe(":"+cfg.Port, r); err != nil {
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
	database.Close()
	log.Println("Goodbye!")
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
	fmt.Println("     Browser will open automatically.")
	fmt.Println("     Press Ctrl+C to stop the server.")
	fmt.Println()
	fmt.Println("  ================================================")
	fmt.Println()
}
