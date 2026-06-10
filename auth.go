package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type AuthData struct {
	PasswordHash string `json:"password_hash"`
	Salt         string `json:"salt"`
}

type sessionEntry struct {
	Token   string    `json:"token"`
	Expiry  time.Time `json:"expiry"`
}

type AuthManager struct {
	dataDir  string
	sessions map[string]time.Time
	mu       sync.RWMutex
}

func NewAuthManager(dataDir string) *AuthManager {
	am := &AuthManager{
		dataDir:  dataDir,
		sessions: make(map[string]time.Time),
	}
	am.loadSessions()
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

func (am *AuthManager) IsSetup() bool {
	_, err := os.Stat(am.authFilePath())
	return err == nil
}

func (am *AuthManager) Setup(password string) error {
	if am.IsSetup() {
		return fmt.Errorf("password already configured")
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}

	saltHex := hex.EncodeToString(salt)
	hash := hashPassword(password, saltHex)

	data := AuthData{
		PasswordHash: hash,
		Salt:         saltHex,
	}

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(am.authFilePath(), b, 0600)
}

func (am *AuthManager) Verify(password string) bool {
	b, err := os.ReadFile(am.authFilePath())
	if err != nil {
		return false
	}

	var data AuthData
	if err := json.Unmarshal(b, &data); err != nil {
		return false
	}

	return hashPassword(password, data.Salt) == data.PasswordHash
}

func (am *AuthManager) ResetPassword(newPassword string) error {
	if !am.IsSetup() {
		return fmt.Errorf("password not configured yet, please start the server and set up via web UI first")
	}

	if len(newPassword) < 6 {
		return fmt.Errorf("new password must be at least 6 characters")
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}

	saltHex := hex.EncodeToString(salt)
	hash := hashPassword(newPassword, saltHex)

	data := AuthData{
		PasswordHash: hash,
		Salt:         saltHex,
	}

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(am.authFilePath(), b, 0600)
}

func (am *AuthManager) ChangePassword(oldPassword, newPassword string) error {
	if !am.Verify(oldPassword) {
		return fmt.Errorf("invalid old password")
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}

	saltHex := hex.EncodeToString(salt)
	hash := hashPassword(newPassword, saltHex)

	data := AuthData{
		PasswordHash: hash,
		Salt:         saltHex,
	}

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(am.authFilePath(), b, 0600)
}

func (am *AuthManager) CreateSession() string {
	token := make([]byte, 32)
	rand.Read(token)
	tokenStr := hex.EncodeToString(token)

	am.mu.Lock()
	am.cleanExpiredSessions()
	am.sessions[tokenStr] = time.Now().Add(24 * time.Hour)
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

func (am *AuthManager) RefreshSession(token string) {
	if token == "" {
		return
	}
	am.mu.Lock()
	if _, ok := am.sessions[token]; ok {
		am.sessions[token] = time.Now().Add(24 * time.Hour)
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

		// Refresh session on each valid request
		am.RefreshSession(token)

		next.ServeHTTP(w, r)
	})
}

func hashPassword(password, salt string) string {
	h := sha256.New()
	h.Write([]byte(salt + password))
	return hex.EncodeToString(h.Sum(nil))
}
