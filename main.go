package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

//go:embed static/*
var staticFiles embed.FS

func main() {
	resetPassword := flag.String("reset-password", "", "Reset the login password (requires new password as value)")
	flag.Parse()

	// Handle password reset command
	if *resetPassword != "" {
		dataDir := os.Getenv("DATA_DIR")
		if dataDir == "" {
			dataDir = "/app/data"
		}
		authMgr := NewAuthManager(dataDir)
		if err := authMgr.ResetPassword(*resetPassword); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Password has been reset successfully!")
		os.Exit(0)
	}

	port := os.Getenv("WEB_PORT")
	if port == "" {
		port = "7500"
	}

	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "/app/data"
	}

	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// Initialize managers
	configMgr := NewConfigManager(dataDir)
	processMgr := NewProcessManager(dataDir, configMgr)
	authMgr := NewAuthManager(dataDir)

	// Create handler
	handler := NewHandler(configMgr, processMgr, authMgr)

	// Setup routes
	mux := http.NewServeMux()

	// Request logging middleware wrapper
	loggedMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[%s] %s %s", r.RemoteAddr, r.Method, r.URL.Path)
		mux.ServeHTTP(w, r)
	})

	// Health check (no auth)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]string{"status": "ok", "version": "v1.3.0"})
	})

	// Auth routes (no auth middleware)
	mux.HandleFunc("GET /api/auth/status", handler.AuthStatus)
	mux.HandleFunc("POST /api/auth/setup", handler.AuthSetup)
	mux.HandleFunc("POST /api/auth/login", handler.AuthLogin)
	mux.HandleFunc("POST /api/auth/send-code", handler.AuthSendCode)

	// Protected API routes
	mux.Handle("POST /api/auth/logout", authMgr.Middleware(http.HandlerFunc(handler.AuthLogout)))
	mux.Handle("POST /api/auth/change-credentials", authMgr.Middleware(http.HandlerFunc(handler.AuthChangeCredentials)))
	mux.Handle("GET /api/auth/bark", authMgr.Middleware(http.HandlerFunc(handler.AuthBarkStatus)))
	mux.Handle("POST /api/auth/bark", authMgr.Middleware(http.HandlerFunc(handler.AuthBarkSet)))
	mux.Handle("POST /api/auth/bark/enabled", authMgr.Middleware(http.HandlerFunc(handler.AuthBarkSetEnabled)))
	mux.Handle("DELETE /api/auth/bark", authMgr.Middleware(http.HandlerFunc(handler.AuthBarkClear)))
	mux.Handle("POST /api/auth/bark/test", authMgr.Middleware(http.HandlerFunc(handler.AuthBarkTest)))
	mux.Handle("GET /api/servers", authMgr.Middleware(http.HandlerFunc(handler.ListServers)))
	mux.Handle("POST /api/servers", authMgr.Middleware(http.HandlerFunc(handler.CreateServer)))
	mux.Handle("PUT /api/servers/{id}", authMgr.Middleware(http.HandlerFunc(handler.UpdateServer)))
	mux.Handle("DELETE /api/servers/{id}", authMgr.Middleware(http.HandlerFunc(handler.DeleteServer)))

	mux.Handle("GET /api/servers/{id}/proxies", authMgr.Middleware(http.HandlerFunc(handler.ListProxies)))
	mux.Handle("POST /api/servers/{id}/proxies", authMgr.Middleware(http.HandlerFunc(handler.CreateProxy)))
	mux.Handle("PUT /api/servers/{id}/proxies/{pid}", authMgr.Middleware(http.HandlerFunc(handler.UpdateProxy)))
	mux.Handle("DELETE /api/servers/{id}/proxies/{pid}", authMgr.Middleware(http.HandlerFunc(handler.DeleteProxy)))
	mux.Handle("POST /api/servers/{id}/proxies/{pid}/toggle", authMgr.Middleware(http.HandlerFunc(handler.ToggleProxy)))

	mux.Handle("GET /api/export", authMgr.Middleware(http.HandlerFunc(handler.ExportConfig)))
	mux.Handle("POST /api/import", authMgr.Middleware(http.HandlerFunc(handler.ImportConfig)))

	mux.Handle("POST /api/servers/{id}/start", authMgr.Middleware(http.HandlerFunc(handler.StartServer)))
	mux.Handle("POST /api/servers/{id}/stop", authMgr.Middleware(http.HandlerFunc(handler.StopServer)))
	mux.Handle("POST /api/servers/{id}/restart", authMgr.Middleware(http.HandlerFunc(handler.RestartServer)))
	mux.Handle("GET /api/servers/{id}/status", authMgr.Middleware(http.HandlerFunc(handler.ServerStatus)))
	mux.Handle("GET /api/servers/{id}/logs", authMgr.Middleware(http.HandlerFunc(handler.ServerLogs)))
	mux.Handle("GET /api/servers/{id}/config", authMgr.Middleware(http.HandlerFunc(handler.ServerConfig)))
	mux.Handle("DELETE /api/servers/{id}/logs", authMgr.Middleware(http.HandlerFunc(handler.ClearServerLogs)))

	// Static files (embedded in binary)
	staticSub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("Failed to load embedded static files: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticSub)))

	// Auto-start servers
	servers, err := configMgr.Load()
	if err != nil {
		log.Printf("Failed to load servers for auto-start: %v", err)
	} else {
		for _, server := range servers {
			// AutoStart is nil (default) or true -> start
			if server.AutoStart == nil || *server.AutoStart {
				log.Printf("Auto-starting server: %s", server.Name)
				toml := configMgr.GenerateToml(&server)
				if err := processMgr.Start(server.ID, toml); err != nil {
					log.Printf("Failed to auto-start server %s: %v", server.Name, err)
				}
			}
		}
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down gracefully...")
		processMgr.StopAll()
		os.Exit(0)
	}()

	log.Printf("frpc-webui starting on port %s", port)
	log.Printf("Data directory: %s", dataDir)
	if err := http.ListenAndServe(fmt.Sprintf(":%s", port), loggedMux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
