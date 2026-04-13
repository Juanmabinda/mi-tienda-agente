package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const version = "0.5.0"
const defaultServer = "https://mitienda.app"

// Pairing alphabet — uppercase, no ambiguous chars (no 0/O, 1/I/L)
const pairingAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// RemoteConfig supports both v1 (single printer) and v2 (multi printer)
// formats. The server determines which to send based on the auth token.
type RemoteConfig struct {
	Server struct {
		WebsocketURL string `json:"websocket_url"`
	} `json:"server"`
	// v2 fields
	ClubID           int             `json:"club_id,omitempty"`
	PrimaryPrinterID int             `json:"primary_printer_id,omitempty"`
	Printers         []PrinterConfig `json:"printers,omitempty"`
	// v1 legacy fields
	Printer struct {
		Brand string `json:"brand"`
		IP    string `json:"ip"`
		Port  int    `json:"port"`
		Model string `json:"model"`
	} `json:"printer,omitempty"`
	Device struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		PointOfSale int    `json:"point_of_sale"`
	} `json:"device,omitempty"`
}

// IsV2 reports whether the response came from the multi-printer endpoint.
// We can't rely on len(Printers) > 0 because a freshly paired club may
// have zero printers configured yet but still be a valid v2 club.
func (c *RemoteConfig) IsV2() bool {
	return c.ClubID > 0
}

func main() {
	handleInstall()
	fmt.Println("======================================")
	fmt.Println("   Mi Tienda Print Agent v" + version)
	fmt.Println("======================================")
	fmt.Println()

	serverURL := findServerURL()
	log.Printf("Server: %s", serverURL)

	token := readTokenFile()
	if token == "" {
		token = doPairing(serverURL)
		saveTokenFile(token)
		// First successful pairing → install autostart so the agent runs at boot
		autoInstallIfNeeded()
		fmt.Println()
		log.Println("Agente conectado y configurado para arrancar al iniciar la PC.")
	} else {
		log.Printf("Token: %s...%s", token[:8], token[len(token)-4:])
	}

	log.Println("Conectando con Mi Tienda...")
	config, err := fetchConfigV2OrV1(serverURL, token)
	if err != nil {
		fmt.Printf("\nError: %v\n", err)
		fmt.Println("Verifica que el token sea correcto.")
		deleteTokenFile()
		waitAndExit(1)
	}

	var agentRunner interface {
		Run()
		Close()
	}

	if config.IsV2() {
		log.Printf("Modo multi-impresora (v2), %d impresoras configuradas", len(config.Printers))
		pool := buildPool(config.Printers)
		agentRunner = &AgentV2{
			token:        token,
			serverURL:    serverURL,
			websocketURL: config.Server.WebsocketURL,
			primaryID:    config.PrimaryPrinterID,
			pool:         pool,
		}
	} else {
		log.Printf("Modo legacy (v1) - Dispositivo: %s (PV %d)", config.Device.Name, config.Device.PointOfSale)
		log.Printf("Impresora: %s %s en %s:%d", config.Printer.Brand, config.Printer.Model, config.Printer.IP, config.Printer.Port)

		printer := NewPrinter(config.Printer.Brand, config.Printer.IP, config.Printer.Port, 15)
		if status, err := printer.Status(); err != nil {
			log.Printf("Impresora no accesible: %v (se reintentara al imprimir)", err)
		} else {
			log.Printf("Impresora OK: modelo=%s", status["model"])
		}
		agentRunner = &Agent{
			token:        token,
			websocketURL: config.Server.WebsocketURL,
			printer:      printer,
		}
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	go func() {
		<-interrupt
		log.Println("Apagando...")
		agentRunner.Close()
		os.Exit(0)
	}()

	fmt.Println()
	log.Println("Agente listo. Esperando trabajos de impresion...")
	fmt.Println("(Ctrl+C para salir)")
	fmt.Println()

	agentRunner.Run()
}

// generatePairingCode returns a 6-character code from the unambiguous alphabet.
func generatePairingCode() string {
	b := make([]byte, 6)
	rand.Read(b)
	out := make([]byte, 6)
	for i := 0; i < 6; i++ {
		out[i] = pairingAlphabet[int(b[i])%len(pairingAlphabet)]
	}
	return string(out)
}

// doPairing generates a code, registers it with the server, shows it to the
// user, and polls until the server reports the code claimed by a club.
// Returns the agent token issued by the server.
func doPairing(serverURL string) string {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "Computadora"
	}

	code := generatePairingCode()

	if err := registerPairingCode(serverURL, code, hostname); err != nil {
		fmt.Printf("\nNo se pudo contactar a Mi Tienda: %v\n", err)
		fmt.Println("Verifica tu conexion a internet e intenta de nuevo.")
		waitAndExit(1)
	}

	printPairingCode(code)

	codeIssuedAt := time.Now()
	for {
		time.Sleep(3 * time.Second)

		// Refresh the code if it's about to expire (server TTL is 15 min)
		if time.Since(codeIssuedAt) > 12*time.Minute {
			code = generatePairingCode()
			if err := registerPairingCode(serverURL, code, hostname); err == nil {
				codeIssuedAt = time.Now()
				printPairingCode(code)
			}
			continue
		}

		token, err := pollPairing(serverURL, code)
		if err != nil {
			continue
		}
		if token != "" {
			fmt.Println()
			fmt.Println("¡Conectado! Token recibido.")
			return token
		}
	}
}

func registerPairingCode(serverURL, code, hostname string) error {
	body, _ := json.Marshal(map[string]string{"code": code, "hostname": hostname})
	resp, err := http.Post(serverURL+"/api/agent/pairing", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// pollPairing returns ("", nil) while pending, (token, nil) once paired.
func pollPairing(serverURL, code string) (string, error) {
	resp, err := http.Get(serverURL + "/api/agent/pairing/" + code)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusNotFound:
		return "", nil
	case http.StatusOK:
		var data struct {
			Token string `json:"token"`
		}
		json.NewDecoder(resp.Body).Decode(&data)
		return data.Token, nil
	default:
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
}

func printPairingCode(code string) {
	bar := strings.Repeat("=", 44)
	fmt.Println()
	fmt.Println(bar)
	fmt.Println("   CONECTAR ESTE AGENTE A TU CLUB")
	fmt.Println(bar)
	fmt.Println()
	fmt.Println("   Ingresa este codigo en Mi Tienda:")
	fmt.Println()
	fmt.Println("                  " + code)
	fmt.Println()
	fmt.Println("   1. Abri mitienda.app")
	fmt.Println("   2. Configuracion -> Impresoras")
	fmt.Println("   3. Pega el codigo en \"Conectar agente\"")
	fmt.Println()
	fmt.Println(bar)
	fmt.Println()
	fmt.Println("Esperando confirmacion...")
}

func readTokenFile() string {
	for _, path := range tokenFilePaths() {
		if data, err := os.ReadFile(path); err == nil {
			if t := strings.TrimSpace(string(data)); t != "" {
				return t
			}
		}
	}
	return ""
}

// saveTokenFile writes the token to the OS-specific user data directory,
// creating it if needed. Prefers ~/Library/Application Support/Mi Tienda Print/
// (Mac), %APPDATA%\Mi Tienda Print\ (Windows), ~/.config/mitienda-print/
// (Linux). Falls back to next-to-executable if user data is unwritable.
func saveTokenFile(token string) {
	candidates := userDataTokenPaths()
	candidates = append(candidates, tokenFilePaths()...)

	var lastErr error
	for _, path := range candidates {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			lastErr = err
			continue
		}
		if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
			lastErr = err
			continue
		}
		log.Printf("Token saved to %s", path)
		return
	}
	log.Printf("Warning: could not save token anywhere: %v", lastErr)
}

// userDataTokenPaths returns the preferred locations for storing the token,
// in priority order. These are stable across .app moves and PC restarts.
func userDataTokenPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		return []string{filepath.Join(home, "Library", "Application Support", "Mi Tienda Print", "token.txt")}
	case "windows":
		paths := []string{}
		if appData := os.Getenv("APPDATA"); appData != "" {
			paths = append(paths, filepath.Join(appData, "Mi Tienda Print", "token.txt"))
		}
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			paths = append(paths, filepath.Join(local, "Mi Tienda Print", "token.txt"))
		}
		return paths
	default:
		return []string{filepath.Join(home, ".config", "mitienda-print", "token.txt")}
	}
}

func deleteTokenFile() {
	for _, path := range tokenFilePaths() {
		os.Remove(path)
	}
}

func tokenFilePaths() []string {
	paths := []string{"token.txt"}

	// Standard user data location (preferred for installer-managed tokens)
	if home, err := os.UserHomeDir(); err == nil {
		switch runtime.GOOS {
		case "darwin":
			paths = append([]string{filepath.Join(home, "Library", "Application Support", "Mi Tienda Print", "token.txt")}, paths...)
		case "windows":
			if appData := os.Getenv("APPDATA"); appData != "" {
				paths = append([]string{filepath.Join(appData, "Mi Tienda Print", "token.txt")}, paths...)
			}
			if local := os.Getenv("LOCALAPPDATA"); local != "" {
				paths = append([]string{filepath.Join(local, "Mi Tienda Print", "token.txt")}, paths...)
			}
		default: // linux
			paths = append([]string{filepath.Join(home, ".config", "mitienda-print", "token.txt")}, paths...)
		}
	}

	// Next to executable
	if exePath, err := os.Executable(); err == nil {
		paths = append([]string{filepath.Join(filepath.Dir(exePath), "token.txt")}, paths...)

		// On Mac, if running from a .app bundle, also check parent of bundle
		// exePath: .../Foo.app/Contents/MacOS/binary
		// We want: .../  (parent of Foo.app) — for installer-bundled token.txt sibling
		if runtime.GOOS == "darwin" {
			macOSDir := filepath.Dir(exePath)
			contentsDir := filepath.Dir(macOSDir)
			appDir := filepath.Dir(contentsDir)
			if filepath.Ext(appDir) == ".app" {
				bundleParent := filepath.Dir(appDir)
				paths = append([]string{filepath.Join(bundleParent, "token.txt")}, paths...)
			}
		}
	}

	return paths
}

func findServerURL() string {
	if v := os.Getenv("MI_TIENDA_URL"); v != "" {
		return v
	}
	return defaultServer
}

func fetchConfig(serverURL, token string) (*RemoteConfig, error) {
	return fetchConfigFromPath(serverURL, token, "/api/fiscal-agent/config")
}

// fetchConfigV2OrV1 tries v2 first, falls back to v1 if v2 returned an error
// (e.g. legacy fiscal-only token that the new endpoint doesn't recognize).
func fetchConfigV2OrV1(serverURL, token string) (*RemoteConfig, error) {
	cfg, err := fetchConfigFromPath(serverURL, token, "/api/agent/config")
	if err == nil {
		return cfg, nil
	}
	// Fallback to legacy
	return fetchConfigFromPath(serverURL, token, "/api/fiscal-agent/config")
}

func fetchConfigFromPath(serverURL, token, path string) (*RemoteConfig, error) {
	req, _ := http.NewRequest("GET", serverURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("token invalido o dispositivo no encontrado")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("error del servidor (HTTP %d)", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var config RemoteConfig
	if err := json.Unmarshal(body, &config); err != nil {
		return nil, fmt.Errorf("respuesta invalida: %w", err)
	}

	return &config, nil
}

func waitAndExit(code int) {
	fmt.Print("\nPresiona Enter para salir...")
	bufio.NewReader(os.Stdin).ReadString('\n')
	os.Exit(code)
}

type Agent struct {
	token        string
	websocketURL string
	printer      FiscalPrinter
	conn         *websocket.Conn
}

func (a *Agent) Run() {
	for {
		err := a.connect()
		if err != nil {
			log.Printf("Conexion perdida: %v", err)
		}
		log.Println("Reconectando en 5s...")
		time.Sleep(5 * time.Second)
	}
}

func (a *Agent) Close() {
	if a.conn != nil {
		a.conn.Close()
	}
}

func (a *Agent) connect() error {
	u, err := url.Parse(a.websocketURL)
	if err != nil {
		return fmt.Errorf("URL invalida: %w", err)
	}
	q := u.Query()
	q.Set("agent_token", a.token)
	u.RawQuery = q.Encode()

	log.Printf("Conectando a %s...", a.websocketURL)
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("no se pudo conectar: %w", err)
	}
	a.conn = conn
	log.Println("Conectado a Mi Tienda")
	a.subscribe()

	heartbeat := time.NewTicker(60 * time.Second)
	defer heartbeat.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			a.handleMessage(message)
		}
	}()

	for {
		select {
		case <-done:
			return fmt.Errorf("conexion cerrada")
		case <-heartbeat.C:
			a.sendAction("heartbeat", nil)
		}
	}
}

func (a *Agent) subscribe() {
	identifier, _ := json.Marshal(map[string]string{
		"channel":       "FiscalAgentChannel",
		"agent_version": version,
	})
	msg := map[string]string{
		"command":    "subscribe",
		"identifier": string(identifier),
	}
	data, _ := json.Marshal(msg)
	a.conn.WriteMessage(websocket.TextMessage, data)
}

func (a *Agent) handleMessage(raw []byte) {
	var msg map[string]interface{}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	msgType, _ := msg["type"].(string)
	switch msgType {
	case "welcome", "ping":
		return
	case "confirm_subscription":
		log.Println("Suscripto al canal fiscal")
		return
	case "reject_subscription":
		log.Println("Suscripcion rechazada")
		return
	}
	message, ok := msg["message"].(map[string]interface{})
	if !ok {
		return
	}
	if message["type"] == "fiscal.print.request" {
		a.handlePrintRequest(message)
	}
}

func (a *Agent) handlePrintRequest(message map[string]interface{}) {
	invoiceID := message["invoice_id"]
	document, _ := message["document"].(map[string]interface{})
	log.Printf("Trabajo de impresion: invoice=%v", invoiceID)

	if a.printer.HasPendingDocument() {
		log.Println("Cancelando documento pendiente...")
		a.printer.CancelPending()
	}

	result, err := a.printer.PrintTicket(document)
	if err != nil {
		log.Printf("Error de impresion: %v", err)
		a.sendAction("print_error", map[string]interface{}{
			"invoice_id":    invoiceID,
			"error_code":    "PRINT_ERROR",
			"error_message": err.Error(),
			"retryable":     true,
		})
		return
	}

	fiscalNumber := fmt.Sprintf("%05v-%08v", result["point_of_sale"], result["fiscal_number"])
	log.Printf("Impreso: %s CAE=%v", fiscalNumber, result["cae"])
	a.sendAction("print_result", map[string]interface{}{
		"invoice_id":       invoiceID,
		"fiscal_number":    fiscalNumber,
		"printer_response": result,
	})
}

func (a *Agent) sendAction(action string, data map[string]interface{}) {
	if a.conn == nil {
		return
	}
	payload := map[string]interface{}{"action": action}
	for k, v := range data {
		payload[k] = v
	}
	identifier, _ := json.Marshal(map[string]string{"channel": "FiscalAgentChannel"})
	payloadJSON, _ := json.Marshal(payload)
	msg := map[string]string{
		"command":    "message",
		"identifier": string(identifier),
		"data":       string(payloadJSON),
	}
	msgJSON, _ := json.Marshal(msg)
	a.conn.WriteMessage(websocket.TextMessage, msgJSON)
}
