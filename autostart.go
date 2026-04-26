package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func handleInstall() {
	if len(os.Args) < 2 {
		return
	}
	switch os.Args[1] {
	case "--install":
		doInstall()
		os.Exit(0)
	case "--uninstall":
		doUninstall()
		os.Exit(0)
	}
}

func autoInstallIfNeeded() {
	if isInstalled() {
		return
	}
	doInstall()
	log.Println("Agente configurado para iniciar automaticamente con la PC.")
}

func isInstalled() bool {
	switch runtime.GOOS {
	case "windows":
		startupDir := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
		_, err := os.Stat(filepath.Join(startupDir, "Mi Tienda Print Agent.vbs"))
		return err == nil
	case "darwin":
		homeDir, _ := os.UserHomeDir()
		_, err := os.Stat(filepath.Join(homeDir, "Library", "LaunchAgents", "app.mitienda.print-agent.plist"))
		return err == nil
	default:
		homeDir, _ := os.UserHomeDir()
		_, err := os.Stat(filepath.Join(homeDir, ".config", "autostart", "mitienda-print.desktop"))
		return err == nil
	}
}

func doInstall() {
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("No se pudo determinar la ruta del ejecutable: %v", err)
		return
	}
	exePath, _ = filepath.Abs(exePath)

	stablePath := ensureStableExe(exePath)
	if stablePath != "" {
		exePath = stablePath
	}

	switch runtime.GOOS {
	case "windows":
		installWindows(exePath)
	case "darwin":
		installMac(exePath)
	default:
		installLinux(exePath)
	}
}

// ensureStableExe copies the executable to a stable user-local path so
// autostart doesn't break if the user deletes the original download.
func ensureStableExe(currentExe string) string {
	if runtime.GOOS == "darwin" {
		return ""
	}

	target := stableExePath()
	if target == "" {
		return ""
	}
	if filepath.Clean(currentExe) == filepath.Clean(target) {
		return target
	}

	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		log.Printf("No se pudo crear directorio de instalacion: %v", err)
		return ""
	}
	if err := copyFile(currentExe, target); err != nil {
		fmt.Println()
		fmt.Println("==========================================================")
		fmt.Println(" ⚠  No se pudo actualizar el agente en la ruta estable.")
		fmt.Printf("    Probablemente hay una version anterior corriendo en:\n    %s\n", target)
		fmt.Println()
		fmt.Println("    Para actualizar:")
		fmt.Println("      1. Cerra la version vieja.")
		fmt.Println("      2. Volve a ejecutar este archivo.")
		fmt.Println("==========================================================")
		fmt.Println()
		log.Printf("copyFile error: %v", err)
		return ""
	}
	_ = os.Chmod(target, 0755)
	log.Printf("Agente instalado en: %s", target)
	return target
}

func stableExePath() string {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return ""
		}
		return filepath.Join(base, "MiTiendaPrint", "mi-tienda-print.exe")
	case "linux":
		home, _ := os.UserHomeDir()
		if home == "" {
			return ""
		}
		return filepath.Join(home, ".local", "share", "mitienda-print", "mi-tienda-print")
	default:
		return ""
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func doUninstall() {
	switch runtime.GOOS {
	case "windows":
		path := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "Mi Tienda Print Agent.vbs")
		os.Remove(path)
		fmt.Println("Inicio automatico desactivado.")
	case "darwin":
		homeDir, _ := os.UserHomeDir()
		path := filepath.Join(homeDir, "Library", "LaunchAgents", "app.mitienda.print-agent.plist")
		os.Remove(path)
		fmt.Println("Inicio automatico desactivado.")
	default:
		homeDir, _ := os.UserHomeDir()
		path := filepath.Join(homeDir, ".config", "autostart", "mitienda-print.desktop")
		os.Remove(path)
		fmt.Println("Inicio automatico desactivado.")
	}
}

func installWindows(exePath string) {
	startupDir := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	vbsPath := filepath.Join(startupDir, "Mi Tienda Print Agent.vbs")

	script := fmt.Sprintf(`Set WshShell = CreateObject("WScript.Shell")
WshShell.Run """%s""", 0, False
`, exePath)

	if err := os.WriteFile(vbsPath, []byte(script), 0644); err != nil {
		log.Printf("No se pudo configurar inicio automatico: %v", err)
		return
	}

	createDesktopShortcutsWindows(exePath)
}

func createDesktopShortcutsWindows(exePath string) {
	desktop := filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
	if _, err := os.Stat(desktop); err != nil {
		return
	}

	urlPath := filepath.Join(desktop, "Mi Tienda.url")
	urlContent := "[InternetShortcut]\r\nURL=https://mitiendapos.com.ar\r\n"
	if err := os.WriteFile(urlPath, []byte(urlContent), 0644); err != nil {
		log.Printf("No se pudo crear acceso directo: %v", err)
	}

	lnkPath := filepath.Join(desktop, "Mi Tienda Print.lnk")
	workingDir := filepath.Dir(exePath)
	psScript := fmt.Sprintf(`$s=(New-Object -COM WScript.Shell).CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.Description='Mi Tienda Print Agent';$s.Save()`,
		lnkPath, exePath, workingDir)
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	if err := cmd.Run(); err != nil {
		log.Printf("No se pudo crear acceso directo al agente: %v", err)
	}
}

func installMac(exePath string) {
	homeDir, _ := os.UserHomeDir()
	plistDir := filepath.Join(homeDir, "Library", "LaunchAgents")
	os.MkdirAll(plistDir, 0755)
	plistPath := filepath.Join(plistDir, "app.mitienda.print-agent.plist")
	logPath := filepath.Join(homeDir, "Library", "Logs", "mitienda-print.log")

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>app.mitienda.print-agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, exePath, logPath, logPath)

	if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
		log.Printf("No se pudo configurar inicio automatico: %v", err)
	}
}

func installLinux(exePath string) {
	homeDir, _ := os.UserHomeDir()
	autostartDir := filepath.Join(homeDir, ".config", "autostart")
	os.MkdirAll(autostartDir, 0755)
	desktopPath := filepath.Join(autostartDir, "mitienda-print.desktop")

	desktop := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Mi Tienda Print Agent
Exec=%s
Hidden=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
`, exePath)

	if err := os.WriteFile(desktopPath, []byte(desktop), 0644); err != nil {
		log.Printf("No se pudo configurar inicio automatico: %v", err)
	}
}
