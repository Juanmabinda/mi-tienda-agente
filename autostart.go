package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

// handleInstall se llama con --install o --uninstall explícito
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

// autoInstallIfNeeded se llama después de conectar exitosamente.
// Si no está instalado como servicio de inicio, lo instala silenciosamente.
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
		_, err := os.Stat(filepath.Join(startupDir, "Mi Tienda Fiscal Agent.vbs"))
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

	switch runtime.GOOS {
	case "windows":
		installWindows(exePath)
	case "darwin":
		installMac(exePath)
	default:
		installLinux(exePath)
	}
}

func doUninstall() {
	switch runtime.GOOS {
	case "windows":
		path := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "Mi Tienda Fiscal Agent.vbs")
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
	vbsPath := filepath.Join(startupDir, "Mi Tienda Fiscal Agent.vbs")

	script := fmt.Sprintf(`Set WshShell = CreateObject("WScript.Shell")
WshShell.Run """%s""", 0, False
`, exePath)

	if err := os.WriteFile(vbsPath, []byte(script), 0644); err != nil {
		log.Printf("No se pudo configurar inicio automatico: %v", err)
		return
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
Name=Mi Tienda Fiscal Agent
Exec=%s
Hidden=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
`, exePath)

	if err := os.WriteFile(desktopPath, []byte(desktop), 0644); err != nil {
		log.Printf("No se pudo configurar inicio automatico: %v", err)
	}
}
