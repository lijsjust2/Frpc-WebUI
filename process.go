package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	maxLogSize          = 10 * 1024 * 1024 // 10MB
	healthCheckInterval = 30 * time.Second
)

type ProcessInfo struct {
	ServerID    string
	Cmd         *exec.Cmd
	LogFile     string
	Running     bool
	TomlContent string
	Done        chan struct{} // closed when cmd.Wait() completes
}

type ProcessManager struct {
	dataDir   string
	configMgr *ConfigManager
	processes map[string]*ProcessInfo
	mu        sync.RWMutex
}

func NewProcessManager(dataDir string, configMgr *ConfigManager) *ProcessManager {
	logsDir := filepath.Join(dataDir, "logs")
	os.MkdirAll(logsDir, 0755)
	confDir := filepath.Join(dataDir, "conf")
	os.MkdirAll(confDir, 0755)

	pm := &ProcessManager{
		dataDir:   dataDir,
		configMgr: configMgr,
		processes: make(map[string]*ProcessInfo),
	}

	// Start health check loop
	go pm.healthCheckLoop()

	return pm
}

func (pm *ProcessManager) healthCheckLoop() {
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()
	for range ticker.C {
		pm.checkAndRestartProcesses()
	}
}

func (pm *ProcessManager) checkAndRestartProcesses() {
	servers, err := pm.configMgr.Load()
	if err != nil {
		return
	}

	for _, server := range servers {
		// Only auto-restart servers with autoStart enabled
		if server.AutoStart != nil && !*server.AutoStart {
			continue
		}

		pm.mu.RLock()
		info, exists := pm.processes[server.ID]
		wasRunning := exists && info.Running
		pm.mu.RUnlock()

		if exists && !wasRunning {
			// Process was running but has exited, try to restart
			toml := pm.configMgr.GenerateToml(&server)
			log.Printf("Health check: restarting crashed server %s", server.Name)
			if err := pm.Start(server.ID, toml); err != nil {
				log.Printf("Health check: failed to restart server %s: %v", server.Name, err)
			}
		}
	}
}

func (pm *ProcessManager) frpcPath() string {
	if path := os.Getenv("FRPC_PATH"); path != "" {
		return path
	}
	name := "frpc"
	if runtime.GOOS == "windows" {
		name = "frpc.exe"
	}
	return filepath.Join(pm.dataDir, "frpc", name)
}

func (pm *ProcessManager) confPath(serverID string) string {
	return filepath.Join(pm.dataDir, "conf", serverID+".toml")
}

func (pm *ProcessManager) logPath(serverID string) string {
	return filepath.Join(pm.dataDir, "logs", serverID+".log")
}

func (pm *ProcessManager) rotateLogIfNeeded(logFile string) {
	info, err := os.Stat(logFile)
	if err != nil || info.Size() < maxLogSize {
		return
	}
	oldFile := logFile + ".old"
	os.Remove(oldFile)
	os.Rename(logFile, oldFile)
}

func (pm *ProcessManager) Start(serverID string, tomlContent string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Check if already running
	if info, ok := pm.processes[serverID]; ok && info.Running {
		return fmt.Errorf("server %s is already running", serverID)
	}

	// Check frpc binary
	frpcPath := pm.frpcPath()
	if _, err := os.Stat(frpcPath); err != nil {
		return fmt.Errorf("frpc binary not found, please install frpc first")
	}

	// Write config file
	confFile := pm.confPath(serverID)
	if err := os.WriteFile(confFile, []byte(tomlContent), 0644); err != nil {
		return fmt.Errorf("failed to write config: %v", err)
	}

	// Rotate log if needed
	logFile := pm.logPath(serverID)
	pm.rotateLogIfNeeded(logFile)

	// Open log file with append mode to preserve history
	lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_SYNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create log file: %v", err)
	}

	// Write separator to distinguish different runs
	separator := fmt.Sprintf("\n========== %s ==========\n", time.Now().Format("2006-01-02 15:04:05"))
	lf.WriteString(separator)

	// Start frpc
	cmd := exec.Command(frpcPath, "-c", confFile)
	cmd.Stdout = lf
	cmd.Stderr = lf

	if err := cmd.Start(); err != nil {
		lf.Close()
		return fmt.Errorf("failed to start frpc: %v", err)
	}

	info := &ProcessInfo{
		ServerID:    serverID,
		Cmd:         cmd,
		LogFile:     logFile,
		Running:     true,
		TomlContent: tomlContent,
		Done:        make(chan struct{}),
	}
	pm.processes[serverID] = info

	// Monitor process in background
	go func() {
		cmd.Wait()
		lf.Close()
		close(info.Done) // Signal that process has fully exited
		pm.mu.Lock()
		if p, ok := pm.processes[serverID]; ok && p.Cmd == cmd {
			p.Running = false
		}
		pm.mu.Unlock()
		log.Printf("frpc process for server %s exited", serverID)
	}()

	log.Printf("frpc started for server %s (PID: %d)", serverID, cmd.Process.Pid)
	return nil
}

func (pm *ProcessManager) Stop(serverID string) error {
	pm.mu.Lock()

	info, ok := pm.processes[serverID]
	if !ok || !info.Running {
		pm.mu.Unlock()
		return nil
	}

	cmd := info.Cmd
	done := info.Done
	info.Running = false
	pm.mu.Unlock()

	// Kill the process
	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("failed to stop frpc: %v", err)
	}

	// Wait for the monitoring goroutine to finish cmd.Wait() and close the log file
	select {
	case <-done:
		// Process cleaned up
	case <-time.After(5 * time.Second):
		log.Printf("warning: timeout waiting for frpc process %s to exit", serverID)
	}

	log.Printf("frpc stopped for server %s", serverID)
	return nil
}

func (pm *ProcessManager) Restart(serverID string, tomlContent string) error {
	if err := pm.Stop(serverID); err != nil {
		return err
	}

	// Wait briefly for process cleanup
	time.Sleep(500 * time.Millisecond)
	return pm.Start(serverID, tomlContent)
}

func (pm *ProcessManager) Status(serverID string) (bool, int) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	info, ok := pm.processes[serverID]
	if !ok {
		return false, 0
	}

	pid := 0
	if info.Cmd != nil && info.Cmd.Process != nil {
		pid = info.Cmd.Process.Pid
	}
	return info.Running, pid
}

func (pm *ProcessManager) GetLogs(serverID string, lines int) (string, error) {
	logFile := pm.logPath(serverID)
	b, err := os.ReadFile(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	content := string(b)

	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	content = ansiRegex.ReplaceAllString(content, "")

	content = strings.TrimSpace(content)

	if lines > 0 {
		allLines := strings.Split(content, "\n")
		if len(allLines) > lines {
			allLines = allLines[len(allLines)-lines:]
		}
		content = strings.Join(allLines, "\n")
	}

	return content, nil
}

func (pm *ProcessManager) ClearLogs(serverID string) error {
	logFile := pm.logPath(serverID)
	err := os.WriteFile(logFile, []byte{}, 0644)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (pm *ProcessManager) StopAll() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for id, info := range pm.processes {
		if info.Running {
			info.Cmd.Process.Kill()
			info.Running = false
			log.Printf("frpc stopped for server %s (shutdown)", id)
		}
	}
}
