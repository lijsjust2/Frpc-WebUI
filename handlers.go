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

func setAuthCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// --- Auth ---

// AuthStatus 返回初始化与 Bark 二次验证的状态，供前端决定渲染哪种登录表单。
func (h *Handler) AuthStatus(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, 200, map[string]interface{}{
		"needSetup":      !h.auth.IsSetup(),
		"barkConfigured": h.auth.IsBarkConfigured(),
		"barkEnabled":    h.auth.IsBarkEnabled(),
	})
}

// AuthSetup 首次使用时设置账号与密码，并立即建立会话。
func (h *Handler) AuthSetup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, 400, "请求参数无效")
		return
	}
	if body.Username == "" || body.Password == "" {
		jsonError(w, 400, "用户名和密码均不能为空")
		return
	}

	if err := h.auth.Setup(body.Username, body.Password); err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	token := h.auth.CreateSession()
	setAuthCookie(w, token)
	jsonResponse(w, 200, map[string]string{"token": token})
}

// AuthSendCode 校验账号密码后通过 Bark 推送验证码。
// 受 60 秒/IP 冷却限制；账号密码错误时不发送验证码。
func (h *Handler) AuthSendCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, 400, "请求参数无效")
		return
	}
	if body.Username == "" || body.Password == "" {
		jsonError(w, 400, "用户名和密码均不能为空")
		return
	}
	if !h.auth.IsBarkEnabled() {
		jsonError(w, 400, "Bark 二次验证未启用，无需获取验证码")
		return
	}
	if !h.auth.Verify(body.Username, body.Password) {
		jsonError(w, 401, "用户名或密码错误")
		return
	}

	ip := clientIP(r)
	if err := h.auth.RequestCode(ip, body.Username); err != nil {
		if wait := AsRateLimitWait(err); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(wait))
			jsonResponse(w, 429, map[string]interface{}{
				"error": err.Error(),
				"wait":  wait,
			})
			return
		}
		jsonError(w, 400, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]interface{}{
		"status":  "sent",
		"message": "验证码已通过 Bark 推送，请查收",
	})
}

// AuthLogin 登录入口。
// - 启用 Bark 时先校验验证码，再校验账号密码；
// - 未启用 Bark 时仅校验账号密码。
func (h *Handler) AuthLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, 400, "请求参数无效")
		return
	}
	if body.Username == "" || body.Password == "" {
		jsonError(w, 400, "用户名和密码均不能为空")
		return
	}

	// 启用 Bark 时先校验验证码
	if h.auth.IsBarkEnabled() {
		if err := h.auth.VerifyCode(clientIP(r), body.Code); err != nil {
			jsonError(w, 401, err.Error())
			return
		}
	}

	if !h.auth.Verify(body.Username, body.Password) {
		jsonError(w, 401, "用户名或密码错误")
		return
	}

	token := h.auth.CreateSession()
	setAuthCookie(w, token)
	jsonResponse(w, 200, map[string]string{"token": token})
}

func (h *Handler) AuthLogout(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Auth-Token")
	if token == "" {
		if cookie, err := r.Cookie("auth_token"); err == nil {
			token = cookie.Value
		}
	}
	if token != "" {
		h.auth.DestroySession(token)
	}
	clearAuthCookie(w)
	jsonResponse(w, 200, map[string]string{"status": "logged out"})
}

// AuthChangeCredentials 修改账号和/或密码。newUsername 为空则不改用户名，newPassword 为空则不改密码。
func (h *Handler) AuthChangeCredentials(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OldPassword string `json:"oldPassword"`
		NewUsername string `json:"newUsername"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, 400, "请求参数无效")
		return
	}
	if body.OldPassword == "" {
		jsonError(w, 400, "请输入当前密码")
		return
	}
	if body.NewUsername == "" && body.NewPassword == "" {
		jsonError(w, 400, "新用户名和新密码不能同时为空")
		return
	}

	if err := h.auth.ChangeCredentials(body.OldPassword, body.NewUsername, body.NewPassword); err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	// 修改凭证后销毁当前会话，强制重新登录
	token := r.Header.Get("X-Auth-Token")
	if token == "" {
		if cookie, err := r.Cookie("auth_token"); err == nil {
			token = cookie.Value
		}
	}
	if token != "" {
		h.auth.DestroySession(token)
	}
	clearAuthCookie(w)

	jsonResponse(w, 200, map[string]string{"status": "credentials changed, please re-login"})
}

// --- Bark 二次验证 ---

// AuthBarkStatus 返回 Bark 配置状态（不返回 Device Token 等敏感字段）。
func (h *Handler) AuthBarkStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.auth.GetBarkConfig()
	resp := map[string]interface{}{
		"configured": cfg != nil,
		"enabled":    h.auth.IsBarkEnabled(),
	}
	if cfg != nil {
		resp["serverUrl"] = cfg.ServerURL
		resp["deviceTokenMasked"] = maskToken(cfg.DeviceToken)
	}
	jsonResponse(w, 200, resp)
}

// AuthBarkSet 保存 Bark 服务地址与 Device Token，保存后默认启用。
func (h *Handler) AuthBarkSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServerURL   string `json:"serverUrl"`
		DeviceToken string `json:"deviceToken"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, 400, "请求参数无效")
		return
	}
	if err := h.auth.SetBarkConfig(body.ServerURL, body.DeviceToken); err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]string{"status": "saved"})
}

// AuthBarkSetEnabled 切换 Bark 二次验证的启用/关闭状态，保留配置信息。
func (h *Handler) AuthBarkSetEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, 400, "请求参数无效")
		return
	}
	if err := h.auth.SetBarkEnabled(body.Enabled); err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]interface{}{
		"status":  "ok",
		"enabled": body.Enabled,
	})
}

// AuthBarkClear 清除 Bark 配置（彻底删除）。
func (h *Handler) AuthBarkClear(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.ClearBarkConfig(); err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]string{"status": "cleared"})
}

// AuthBarkTest 推送一条测试消息。若请求体包含 serverUrl/deviceToken，则使用传入值测试；否则使用已保存的配置。
func (h *Handler) AuthBarkTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServerURL   string `json:"serverUrl"`
		DeviceToken string `json:"deviceToken"`
	}
	var cfg *BarkConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil &&
		strings.TrimSpace(body.ServerURL) != "" && strings.TrimSpace(body.DeviceToken) != "" {
		// 使用传入的临时配置测试
		cfg = &BarkConfig{
			ServerURL:   strings.TrimSpace(body.ServerURL),
			DeviceToken: strings.TrimSpace(body.DeviceToken),
		}
	} else {
		// 使用已保存的配置
		cfg = h.auth.GetBarkConfig()
		if cfg == nil {
			jsonError(w, 400, "Bark 未配置")
			return
		}
	}
	if err := pushBarkCode(cfg, "000000", h.auth.GetUsername(), "测试推送"); err != nil {
		jsonError(w, 400, "测试推送失败："+err.Error())
		return
	}
	jsonResponse(w, 200, map[string]string{"status": "sent"})
}

// maskToken 隐藏 Token 中间部分，仅用于展示。
func maskToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 8 {
		return strings.Repeat("*", len(token))
	}
	return token[:4] + strings.Repeat("*", len(token)-8) + token[len(token)-4:]
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
