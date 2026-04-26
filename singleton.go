package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

func acquireSingleInstanceLock() error {
	lockPath := singletonLockPath()
	if lockPath == "" {
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(lockPath), 0755)

	if data, err := os.ReadFile(lockPath); err == nil {
		if pid, err := strconv.Atoi(string(data)); err == nil {
			if isPidAlive(pid) && pid != os.Getpid() {
				return fmt.Errorf("ya corre otra instancia (PID %d)", pid)
			}
		}
	}

	myPid := []byte(fmt.Sprintf("%d", os.Getpid()))
	if err := os.WriteFile(lockPath, myPid, 0644); err != nil {
		return fmt.Errorf("no se pudo escribir lock file: %w", err)
	}

	registerLockCleanup(lockPath)
	return nil
}

func singletonLockPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(base, "Mi Tienda Print", "agent.pid")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Mi Tienda Print", "agent.pid")
	default:
		return filepath.Join(home, ".config", "mitienda-print", "agent.pid")
	}
}

func isPidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(os.Signal(nil))
	return err == nil
}

func registerLockCleanup(lockPath string) {
	go func() {
		for {
			time.Sleep(1 * time.Hour)
			myPid := []byte(fmt.Sprintf("%d", os.Getpid()))
			_ = os.WriteFile(lockPath, myPid, 0644)
		}
	}()
}
