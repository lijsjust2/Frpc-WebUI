package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	sessionTTL    = 30 * time.Minute // 登录缓存：半小时不操作自动退出
	codeTTL       = 5 * time.Minute  // 验证码有效期 5 分钟
	codeCooldown  = 60 * time.Second // 两次获取验证码的最小间隔
	barkPushLimit = 10 * time.Second // bark 推送超时
	geoLookupTTL  = 4 * time.Second  // IP 归属地查询超时
)

// BarkConfig 存储 Bark 推送服务配置（可选的二次验证）
type BarkConfig struct {
	ServerURL   string `json:"serverUrl"`
	DeviceToken string `json:"deviceToken"`
	Enabled     bool   `json:"enabled"`
}

// AuthData 持久化到 auth.json 的凭证数据
type AuthData struct {
	Username     string      `json:"username"`
	PasswordHash string      `json:"password_hash"`
	Salt         string      `json:"salt"`
	Bark         *BarkConfig `json:"bark,omitempty"`
}

type sessionEntry struct {
	Token  string    `json:"token"`
	Expiry time.Time `json:"expiry"`
}

// codeEntry 一次未消费的验证码
type codeEntry struct {
	Code      string    `json:"code"`
	Expiry    time.Time `json:"expiry"`
	CreatedAt time.Time `json:"createdAt"`
}

type AuthManager struct {
	dataDir      string
	data         AuthData
	dataLoaded   bool
	sessions     map[string]time.Time
	codes        map[string]*codeEntry // 按客户端 IP 索引
	sendCooldown map[string]time.Time  // 按客户端 IP 索引
	mu           sync.RWMutex
}

func NewAuthManager(dataDir string) *AuthManager {
	am := &AuthManager{
		dataDir:      dataDir,
		sessions:     make(map[string]time.Time),
		codes:        make(map[string]*codeEntry),
		sendCooldown: make(map[string]time.Time),
	}
	am.loadSessions()
	am.loadAuthData()
	return am
}

func (am *AuthManager) authFilePath() string {
	return filepath.Join(am.dataDir, "auth.json")
}

func (am *AuthManager) sessionsFilePath() string {
	return filepath.Join(am.dataDir, "sessions.json")
}

func (am *AuthManager) loadSessions() {
	b, err := os.ReadFile(am.sessionsFilePath())
	if err != nil {
		return
	}
	var entries []sessionEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return
	}
	now := time.Now()
	for _, e := range entries {
		if now.Before(e.Expiry) {
			am.sessions[e.Token] = e.Expiry
		}
	}
}

func (am *AuthManager) saveSessions() {
	am.mu.RLock()
	defer am.mu.RUnlock()

	entries := make([]sessionEntry, 0, len(am.sessions))
	now := time.Now()
	for token, expiry := range am.sessions {
		if now.Before(expiry) {
			entries = append(entries, sessionEntry{Token: token, Expiry: expiry})
		}
	}

	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(am.sessionsFilePath(), b, 0600)
}

func (am *AuthManager) loadAuthData() (AuthData, error) {
	am.mu.RLock()
	if am.dataLoaded {
		d := am.data
		am.mu.RUnlock()
		return d, nil
	}
	am.mu.RUnlock()

	am.mu.Lock()
	defer am.mu.Unlock()
	if am.dataLoaded {
		return am.data, nil
	}
	b, err := os.ReadFile(am.authFilePath())
	if err != nil {
		return AuthData{}, err
	}
	var data AuthData
	if err := json.Unmarshal(b, &data); err != nil {
		return AuthData{}, err
	}
	am.data = data
	am.dataLoaded = true
	return am.data, nil
}

func (am *AuthManager) saveAuthData(data AuthData) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(am.authFilePath(), b, 0600); err != nil {
		return err
	}
	am.data = data
	am.dataLoaded = true
	return nil
}

// IsSetup 表示是否已完成初始账号密码设置
func (am *AuthManager) IsSetup() bool {
	data, err := am.loadAuthData()
	if err != nil {
		return false
	}
	return data.Username != "" && data.PasswordHash != ""
}

// IsBarkConfigured 表示是否已配置 Bark 信息（不一定启用）
func (am *AuthManager) IsBarkConfigured() bool {
	data, err := am.loadAuthData()
	if err != nil {
		return false
	}
	return data.Bark != nil && data.Bark.ServerURL != "" && data.Bark.DeviceToken != ""
}

// IsBarkEnabled 表示是否启用了 Bark 二次验证（已配置且 enabled=true）
func (am *AuthManager) IsBarkEnabled() bool {
	data, err := am.loadAuthData()
	if err != nil {
		return false
	}
	return data.Bark != nil && data.Bark.Enabled &&
		data.Bark.ServerURL != "" && data.Bark.DeviceToken != ""
}

// GetBarkConfig 返回 Bark 配置的副本（无配置返回 nil）
func (am *AuthManager) GetBarkConfig() *BarkConfig {
	data, err := am.loadAuthData()
	if err != nil || data.Bark == nil {
		return nil
	}
	b := *data.Bark
	return &b
}

// GetUsername 返回当前配置的用户名（未配置时返回空字符串）
func (am *AuthManager) GetUsername() string {
	data, err := am.loadAuthData()
	if err != nil {
		return ""
	}
	return data.Username
}

// CodeCooldownRemaining 返回指定 IP 距离下次可发送验证码的剩余秒数；
// 无冷却时返回 0。
func (am *AuthManager) CodeCooldownRemaining(clientIP string) int {
	am.mu.RLock()
	defer am.mu.RUnlock()
	last, ok := am.sendCooldown[clientIP]
	if !ok {
		return 0
	}
	remaining := int((codeCooldown - time.Since(last)) / time.Second)
	if remaining < 0 {
		return 0
	}
	return remaining + 1
}

// AsRateLimitWait 尝试从错误中提取冷却剩余秒数，失败返回 0。
func AsRateLimitWait(err error) int {
	if e, ok := err.(*rateLimitError); ok {
		return e.wait
	}
	return 0
}

func (am *AuthManager) Setup(username, password string) error {
	if am.IsSetup() {
		return fmt.Errorf("账号已配置，如需修改请登录后在设置中操作")
	}
	if len(username) < 3 {
		return fmt.Errorf("用户名至少需要 3 个字符")
	}
	if len(password) < 6 {
		return fmt.Errorf("密码至少需要 6 位")
	}

	salt := makeSalt()
	data := AuthData{
		Username:     username,
		PasswordHash: hashPassword(password, salt),
		Salt:         salt,
	}
	return am.saveAuthData(data)
}

// Verify 校验用户名+密码
func (am *AuthManager) Verify(username, password string) bool {
	data, err := am.loadAuthData()
	if err != nil {
		return false
	}
	if data.Username == "" || data.Username != username {
		return false
	}
	return hashPassword(password, data.Salt) == data.PasswordHash
}

// ChangePassword 修改密码（需校验旧密码），用户名保持不变
func (am *AuthManager) ChangePassword(oldPassword, newPassword string) error {
	data, err := am.loadAuthData()
	if err != nil {
		return fmt.Errorf("账号未配置")
	}
	if hashPassword(oldPassword, data.Salt) != data.PasswordHash {
		return fmt.Errorf("旧密码错误")
	}
	if len(newPassword) < 6 {
		return fmt.Errorf("新密码至少需要 6 位")
	}
	salt := makeSalt()
	data.PasswordHash = hashPassword(newPassword, salt)
	data.Salt = salt
	return am.saveAuthData(data)
}

// ChangeCredentials 修改账号和/或密码。newUsername 为空则不改用户名，newPassword 为空则不改密码。
func (am *AuthManager) ChangeCredentials(oldPassword, newUsername, newPassword string) error {
	data, err := am.loadAuthData()
	if err != nil {
		return fmt.Errorf("账号未配置")
	}
	if hashPassword(oldPassword, data.Salt) != data.PasswordHash {
		return fmt.Errorf("旧密码错误")
	}
	newUsername = strings.TrimSpace(newUsername)
	if newUsername != "" && newUsername != data.Username {
		if len(newUsername) < 3 {
			return fmt.Errorf("用户名至少需要 3 个字符")
		}
		data.Username = newUsername
	}
	if newPassword != "" {
		if len(newPassword) < 6 {
			return fmt.Errorf("新密码至少需要 6 位")
		}
		salt := makeSalt()
		data.PasswordHash = hashPassword(newPassword, salt)
		data.Salt = salt
	}
	return am.saveAuthData(data)
}

// ResetPassword 命令行重置密码（保留用户名和 Bark 配置）
func (am *AuthManager) ResetPassword(newPassword string) error {
	data, err := am.loadAuthData()
	if err != nil {
		return fmt.Errorf("账号尚未通过 Web UI 初始化，请先启动服务并完成初始设置")
	}
	if len(newPassword) < 6 {
		return fmt.Errorf("新密码至少需要 6 位")
	}
	salt := makeSalt()
	data.PasswordHash = hashPassword(newPassword, salt)
	data.Salt = salt
	return am.saveAuthData(data)
}

// SetBarkConfig 保存 Bark 配置。若已有配置且 deviceToken 为空，则只更新 serverURL，保留原 token。
// 保存后默认启用二次验证。
func (am *AuthManager) SetBarkConfig(serverURL, deviceToken string) error {
	data, err := am.loadAuthData()
	if err != nil {
		return fmt.Errorf("账号未配置")
	}
	serverURL = strings.TrimSpace(serverURL)
	deviceToken = strings.TrimSpace(deviceToken)
	if serverURL == "" {
		return fmt.Errorf("Bark 服务地址不能为空")
	}
	u, err := url.Parse(serverURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("Bark 服务地址必须是有效的 http(s) URL")
	}
	// 已有配置且 deviceToken 留空：只更新 serverURL，保留原 token 与启用状态
	if deviceToken == "" {
		if data.Bark == nil {
			return fmt.Errorf("Device Token 不能为空")
		}
		data.Bark.ServerURL = serverURL
	} else {
		data.Bark = &BarkConfig{ServerURL: serverURL, DeviceToken: deviceToken, Enabled: true}
	}
	return am.saveAuthData(data)
}

// SetBarkEnabled 切换 Bark 二次验证的启用/关闭状态，保留配置信息
func (am *AuthManager) SetBarkEnabled(enabled bool) error {
	data, err := am.loadAuthData()
	if err != nil {
		return fmt.Errorf("账号未配置")
	}
	if data.Bark == nil || data.Bark.ServerURL == "" || data.Bark.DeviceToken == "" {
		if enabled {
			return fmt.Errorf("请先配置 Bark 服务地址和 Device Token")
		}
		return nil
	}
	data.Bark.Enabled = enabled
	return am.saveAuthData(data)
}

// ClearBarkConfig 清除 Bark 配置（关闭二次验证）
func (am *AuthManager) ClearBarkConfig() error {
	data, err := am.loadAuthData()
	if err != nil {
		return fmt.Errorf("账号未配置")
	}
	data.Bark = nil
	return am.saveAuthData(data)
}

// --- 验证码 ---

// RequestCode 生成验证码并通过 Bark 推送。受 60 秒/IP 冷却限制。
func (am *AuthManager) RequestCode(clientIP, username string) error {
	am.mu.Lock()
	am.cleanCodesLocked()
	if last, ok := am.sendCooldown[clientIP]; ok {
		elapsed := time.Since(last)
		if elapsed < codeCooldown {
			wait := int((codeCooldown-elapsed)/time.Second) + 1
			am.mu.Unlock()
			return &rateLimitError{msg: fmt.Sprintf("请等待 %d 秒后再次获取验证码", wait), wait: wait}
		}
	}
	code := generateCode()
	am.codes[clientIP] = &codeEntry{
		Code:      code,
		Expiry:    time.Now().Add(codeTTL),
		CreatedAt: time.Now(),
	}
	am.sendCooldown[clientIP] = time.Now()
	bark := am.data.Bark
	am.mu.Unlock()

	if bark == nil || bark.ServerURL == "" || bark.DeviceToken == "" {
		return fmt.Errorf("Bark 未配置，无法发送验证码")
	}

	location := lookupIPLocation(clientIP)
	if err := pushBarkCode(bark, code, username, location); err != nil {
		log.Printf("bark push failed for ip=%s user=%s: %v", clientIP, username, err)
		return fmt.Errorf("验证码推送失败：%v", err)
	}
	return nil
}

// VerifyCode 校验并消费验证码
func (am *AuthManager) VerifyCode(clientIP, code string) error {
	if code == "" {
		return fmt.Errorf("请输入验证码")
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	am.cleanCodesLocked()
	entry, ok := am.codes[clientIP]
	if !ok {
		return fmt.Errorf("请先获取验证码")
	}
	if time.Now().After(entry.Expiry) {
		delete(am.codes, clientIP)
		return fmt.Errorf("验证码已过期，请重新获取")
	}
	if entry.Code != code {
		return fmt.Errorf("验证码不正确")
	}
	delete(am.codes, clientIP)
	return nil
}

func (am *AuthManager) cleanCodesLocked() {
	now := time.Now()
	for ip, entry := range am.codes {
		if now.After(entry.Expiry) {
			delete(am.codes, ip)
		}
	}
	for ip, last := range am.sendCooldown {
		if now.Sub(last) > codeCooldown {
			delete(am.sendCooldown, ip)
		}
	}
}

// --- 会话 ---

func (am *AuthManager) CreateSession() string {
	token := make([]byte, 32)
	rand.Read(token)
	tokenStr := hex.EncodeToString(token)

	am.mu.Lock()
	am.cleanExpiredSessions()
	am.sessions[tokenStr] = time.Now().Add(sessionTTL)
	am.mu.Unlock()

	am.saveSessions()
	return tokenStr
}

func (am *AuthManager) DestroySession(token string) {
	if token == "" {
		return
	}
	am.mu.Lock()
	delete(am.sessions, token)
	am.mu.Unlock()
	am.saveSessions()
}

func (am *AuthManager) cleanExpiredSessions() {
	now := time.Now()
	for token, expiry := range am.sessions {
		if now.After(expiry) {
			delete(am.sessions, token)
		}
	}
}

func (am *AuthManager) ValidateSession(token string) bool {
	am.mu.RLock()
	expiry, ok := am.sessions[token]
	am.mu.RUnlock()

	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		am.mu.Lock()
		delete(am.sessions, token)
		am.mu.Unlock()
		am.saveSessions()
		return false
	}
	return true
}

// RefreshSession 续期会话（滑动过期）
func (am *AuthManager) RefreshSession(token string) {
	if token == "" {
		return
	}
	am.mu.Lock()
	if _, ok := am.sessions[token]; ok {
		am.sessions[token] = time.Now().Add(sessionTTL)
		am.mu.Unlock()
		am.saveSessions()
	} else {
		am.mu.Unlock()
	}
}

func (am *AuthManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Auth-Token")
		if token == "" {
			if cookie, err := r.Cookie("auth_token"); err == nil {
				token = cookie.Value
			}
		}

		if !am.ValidateSession(token) {
			w.Header().Set("Content-Type", "application/json")
			b, _ := json.Marshal(map[string]string{"error": "unauthorized"})
			w.WriteHeader(http.StatusUnauthorized)
			w.Write(b)
			return
		}

		// 仅用户主动请求续期会话；后台轮询（X-No-Refresh: 1）不续期，
		// 以确保半小时无操作自动退出。
		if r.Header.Get("X-No-Refresh") != "1" {
			am.RefreshSession(token)
			http.SetCookie(w, &http.Cookie{
				Name:     "auth_token",
				Value:    token,
				Path:     "/",
				MaxAge:   int(sessionTTL.Seconds()),
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		}

		next.ServeHTTP(w, r)
	})
}

// --- 工具函数 ---

func hashPassword(password, salt string) string {
	h := sha256.New()
	h.Write([]byte(salt + password))
	return hex.EncodeToString(h.Sum(nil))
}

func makeSalt() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func generateCode() string {
	var n uint32
	b := make([]byte, 4)
	rand.Read(b)
	n = uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return fmt.Sprintf("%04d", n%10000)
}

// rateLimitError 表示受冷却限制的错误
type rateLimitError struct {
	msg  string
	wait int
}

func (e *rateLimitError) Error() string { return e.msg }

// clientIP 从请求中解析真实客户端 IP
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// lookupIPLocation 查询 IP 归属地，失败时仅返回 IP
func lookupIPLocation(ip string) string {
	if ip == "" || isPrivateIP(ip) {
		if ip == "" {
			return "未知"
		}
		return ip + "/本地网络"
	}
	client := &http.Client{Timeout: geoLookupTTL}
	resp, err := client.Get("http://ip-api.com/json/" + ip + "?lang=zh-CN&fields=status,country,regionName,city,query")
	if err != nil {
		return ip
	}
	defer resp.Body.Close()
	var loc struct {
		Status     string `json:"status"`
		Country    string `json:"country"`
		RegionName string `json:"regionName"`
		City       string `json:"city"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loc); err != nil || loc.Status != "success" {
		return ip
	}
	parts := []string{loc.Country, loc.RegionName, loc.City}
	nonEmpty := parts[:0]
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) == 0 {
		return ip
	}
	return ip + "/" + strings.Join(nonEmpty, "")
}

func isPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

// pushBarkCode 通过 Bark 推送登录验证码提醒
func pushBarkCode(cfg *BarkConfig, code, username, location string) error {
	serverURL := strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
	if serverURL == "" {
		return fmt.Errorf("Bark 服务地址未配置")
	}

	body := fmt.Sprintf(
		"【%s】登录验证码，有效期 5 分钟，请尽快认证\n【%s】正在登录Frpc-WebUI",
		code, location,
	)

	payload := map[string]interface{}{
		"device_key": cfg.DeviceToken,
		"title":      "Frpc-WebUI 登录提醒",
		"body":       body,
		"group":      "Frpc-WebUI",
	}
	b, _ := json.Marshal(payload)

	client := &http.Client{Timeout: barkPushLimit}
	resp, err := client.Post(serverURL+"/push", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Bark 返回 %d: %s", resp.StatusCode, string(rb))
	}
	return nil
}
