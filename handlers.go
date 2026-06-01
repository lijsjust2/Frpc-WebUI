package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type Handler struct {
	config  *ConfigManager
	process *ProcessManager
	auth    *AuthManager
}

func NewHandler(config *ConfigManager, process *ProcessManager, auth *AuthManager) *Handler {
	return &Handler{config: config, process: process, auth: auth}
}

func jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	b, err := json.Marshal(data)
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"internal marshal error"}`))
		return
	}
	w.WriteHeader(status)
	w.Write(b)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonResponse(w, status, map[string]string{"error": msg})
}

// validatePort checks if port number is in valid range (1-65535)
func validatePort(port int) bool {
	return port >= 1 && port <= 65535
}

// validateLocalIP checks if IP address format is valid
func validateLocalIP(ip string) bool {
	if ip == "" {
		return false
	}
	if ip == "localhost" {
		return true
	}
	if strings.Contains(ip, ":") {
		return true
	}
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		num, err := strconv.Atoi(part)
		if err != nil || num < 0 || num > 255 {
			return false
		}
	}
	return true
}

// --- Auth ---

func (h *Handler) AuthStatus(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, 200, map[string]interface{}{
		"needSetup": !h.auth.IsSetup(),
	})
}

func (h *Handler) AuthSetup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password == "" {
		jsonError(w, 400, "password is required")
		return
	}

	if err := h.auth.Setup(body.Password); err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	token := h.auth.CreateSession()
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    token,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
	})
	jsonResponse(w, 200, map[string]string{"token": token})
}

func (h *Handler) AuthLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password == "" {
		jsonError(w, 400, "password is required")
		return
	}

	if !h.auth.Verify(body.Password) {
		jsonError(w, 401, "incorrect password")
		return
	}

	token := h.auth.CreateSession()
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    token,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
	})
	jsonResponse(w, 200, map[string]string{"token": token})
}

func (h *Handler) AuthChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, 400, "invalid request body")
		return
	}
	if body.OldPassword == "" || body.NewPassword == "" {
		jsonError(w, 400, "old and new password are required")
		return
	}
	if len(body.NewPassword) < 6 {
		jsonError(w, 400, "new password must be at least 6 characters")
		return
	}

	if err := h.auth.ChangePassword(body.OldPassword, body.NewPassword); err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	jsonResponse(w, 200, map[string]string{"status": "password changed"})
}

// --- Servers ---

func (h *Handler) ListServers(w http.ResponseWriter, r *http.Request) {
	servers, err := h.config.Load()
	if err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	// Attach running status
	type ServerWithStatus struct {
		ServerConfig
		Running bool `json:"running"`
		PID     int  `json:"pid"`
	}

	result := make([]ServerWithStatus, len(servers))
	for i, s := range servers {
		running, pid := h.process.Status(s.ID)
		result[i] = ServerWithStatus{ServerConfig: s, Running: running, PID: pid}
	}

	jsonResponse(w, 200, result)
}

func (h *Handler) CreateServer(w http.ResponseWriter, r *http.Request) {
	var cfg ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		jsonError(w, 400, "invalid request body")
		return
	}

	if cfg.Name == "" || cfg.ServerAddr == "" || cfg.ServerPort == 0 {
		jsonError(w, 400, "name, serverAddr, and serverPort are required")
		return
	}

	if err := h.config.CreateServer(cfg); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	jsonResponse(w, 201, map[string]string{"status": "created"})
}

func (h *Handler) UpdateServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var cfg ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		jsonError(w, 400, "invalid request body")
		return
	}

	if cfg.Name == "" || cfg.ServerAddr == "" || cfg.ServerPort == 0 {
		jsonError(w, 400, "name, serverAddr, and serverPort are required")
		return
	}

	if err := h.config.UpdateServer(id, cfg); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	jsonResponse(w, 200, map[string]string{"status": "updated"})
}

func (h *Handler) DeleteServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Stop if running
	h.process.Stop(id)

	if err := h.config.DeleteServer(id); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	jsonResponse(w, 200, map[string]string{"status": "deleted"})
}

// --- Proxies ---

func (h *Handler) ListProxies(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	server, err := h.config.GetServer(id)
	if err != nil {
		jsonError(w, 404, err.Error())
		return
	}
	jsonResponse(w, 200, server.Proxies)
}

func (h *Handler) CreateProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var proxy ProxyConfig
	if err := json.NewDecoder(r.Body).Decode(&proxy); err != nil {
		jsonError(w, 400, "invalid request body")
		return
	}

	if proxy.Name == "" || proxy.Type == "" {
		jsonError(w, 400, "name and type are required")
		return
	}

	// Default localIP
	if proxy.LocalIP == "" {
		proxy.LocalIP = "127.0.0.1"
	}

	// Validate local port
	if !validatePort(proxy.LocalPort) {
		jsonError(w, 400, "localPort must be between 1 and 65535")
		return
	}

	// Validate local IP
	if !validateLocalIP(proxy.LocalIP) {
		jsonError(w, 400, "localIP must be a valid IPv4 address")
		return
	}

	// Validate remote port for tcp/udp/p2p
	if (proxy.Type == "tcp" || proxy.Type == "udp" || proxy.Type == "p2p") && proxy.RemotePort > 0 {
		if !validatePort(proxy.RemotePort) {
			jsonError(w, 400, "remotePort must be between 1 and 65535")
			return
		}
	}

	// Validate bandwidth format
	if proxy.BandwidthLimit != "" {
		// Simple format check: should end with KB, MB, GB
		upper := strings.ToUpper(proxy.BandwidthLimit)
		if !strings.HasSuffix(upper, "KB") && !strings.HasSuffix(upper, "MB") && !strings.HasSuffix(upper, "GB") {
			jsonError(w, 400, "bandwidthLimit must include unit (KB, MB, GB), e.g., 1MB")
			return
		}
	}

	if err := h.config.AddProxy(id, proxy); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	jsonResponse(w, 201, map[string]string{"status": "created"})
}

func (h *Handler) UpdateProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pid := r.PathValue("pid")

	var proxy ProxyConfig
	if err := json.NewDecoder(r.Body).Decode(&proxy); err != nil {
		jsonError(w, 400, "invalid request body")
		return
	}

	// Default localIP
	if proxy.LocalIP == "" {
		proxy.LocalIP = "127.0.0.1"
	}

	// Validate local port
	if !validatePort(proxy.LocalPort) {
		jsonError(w, 400, "localPort must be between 1 and 65535")
		return
	}

	// Validate local IP
	if !validateLocalIP(proxy.LocalIP) {
		jsonError(w, 400, "localIP must be a valid IPv4 address")
		return
	}

	// Validate remote port for tcp/udp/p2p
	if (proxy.Type == "tcp" || proxy.Type == "udp" || proxy.Type == "p2p") && proxy.RemotePort > 0 {
		if !validatePort(proxy.RemotePort) {
			jsonError(w, 400, "remotePort must be between 1 and 65535")
			return
		}
	}

	// Validate bandwidth format
	if proxy.BandwidthLimit != "" {
		upper := strings.ToUpper(proxy.BandwidthLimit)
		if !strings.HasSuffix(upper, "KB") && !strings.HasSuffix(upper, "MB") && !strings.HasSuffix(upper, "GB") {
			jsonError(w, 400, "bandwidthLimit must include unit (KB, MB, GB), e.g., 1MB")
			return
		}
	}

	if err := h.config.UpdateProxy(id, pid, proxy); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	jsonResponse(w, 200, map[string]string{"status": "updated"})
}

func (h *Handler) DeleteProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pid := r.PathValue("pid")

	if err := h.config.DeleteProxy(id, pid); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	jsonResponse(w, 200, map[string]string{"status": "deleted"})
}

// --- Process Control ---

func (h *Handler) StartServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	server, err := h.config.GetServer(id)
	if err != nil {
		jsonError(w, 404, err.Error())
		return
	}

	toml := h.config.GenerateToml(server)
	if err := h.process.Start(id, toml); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	jsonResponse(w, 200, map[string]string{"status": "started"})
}

func (h *Handler) StopServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.process.Stop(id); err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]string{"status": "stopped"})
}

func (h *Handler) RestartServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	server, err := h.config.GetServer(id)
	if err != nil {
		jsonError(w, 404, err.Error())
		return
	}

	toml := h.config.GenerateToml(server)
	if err := h.process.Restart(id, toml); err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]string{"status": "restarted"})
}

func (h *Handler) ServerStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	running, pid := h.process.Status(id)
	jsonResponse(w, 200, map[string]interface{}{
		"running": running,
		"pid":     pid,
	})
}

func (h *Handler) ServerLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	logs, err := h.process.GetLogs(id, 200)
	if err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]string{"logs": logs})
}

func (h *Handler) ServerConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	server, err := h.config.GetServer(id)
	if err != nil {
		jsonError(w, 404, err.Error())
		return
	}
	toml := h.config.GenerateToml(server)
	jsonResponse(w, 200, map[string]string{"config": toml})
}

func (h *Handler) ClearServerLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.process.ClearLogs(id); err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]string{"status": "cleared"})
}

// --- Proxy Toggle ---

func (h *Handler) ToggleProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pid := r.PathValue("pid")

	if err := h.config.ToggleProxy(id, pid); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	jsonResponse(w, 200, map[string]string{"status": "toggled"})
}

// --- Export/Import Config ---

func (h *Handler) ExportConfig(w http.ResponseWriter, r *http.Request) {
	servers, err := h.config.ExportAll()
	if err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	b, err := json.MarshalIndent(servers, "", "  ")
	if err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=frpc-webui-backup.json")
	w.WriteHeader(200)
	w.Write(b)
}

func (h *Handler) ImportConfig(w http.ResponseWriter, r *http.Request) {
	var servers []ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&servers); err != nil {
		jsonError(w, 400, "invalid config data")
		return
	}

	if len(servers) == 0 {
		jsonError(w, 400, "no servers in imported config")
		return
	}

	// Stop all running servers first
	currentServers, _ := h.config.Load()
	for _, s := range currentServers {
		h.process.Stop(s.ID)
	}

	if err := h.config.ImportAll(servers); err != nil {
		jsonError(w, 500, err.Error())
		return
	}

	jsonResponse(w, 200, map[string]string{"status": "imported"})
}

