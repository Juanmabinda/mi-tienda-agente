package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// PrinterConfig describes a single printer in the v2 multi-printer config.
type PrinterConfig struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Role           string `json:"role"`
	Kind           string `json:"kind"`
	Connection     string `json:"connection"`
	CommandProfile string `json:"command_profile"`
	Brand          string `json:"brand"`
	Model          string `json:"model"`
	IP             string `json:"ip"`
	Port           int    `json:"port"`
	UsbVendorID    string `json:"usb_vendor_id"`
	UsbProductID   string `json:"usb_product_id"`
	UsbDeviceLabel string `json:"usb_device_label"`
	PaperWidthMM   int    `json:"paper_width_mm"`
	CharsPerLine   int    `json:"chars_per_line"`
	PointOfSale    int    `json:"point_of_sale"`
	Copies         int    `json:"copies"`
	AutoCut        bool   `json:"auto_cut"`
	OpenDrawer     bool   `json:"open_drawer"`
}

// AgentV2 is the new multi-printer agent.
type AgentV2 struct {
	token        string
	serverURL    string
	websocketURL string
	primaryID    int
	pool         *PrinterPool
	conn         *websocket.Conn
	mu           sync.Mutex
}

// refreshPool re-fetches the agent config from the server and rebuilds the
// printer pool. Used when a print job arrives for a printer the agent doesn't
// know about yet (e.g. user added it from the UI after the agent started).
func (a *AgentV2) refreshPool() error {
	cfg, err := fetchConfigV2OrV1(a.serverURL, a.token)
	if err != nil {
		return err
	}
	a.pool = buildPool(cfg.Printers)
	a.primaryID = cfg.PrimaryPrinterID
	log.Printf("Pool refreshed: %d printers loaded", len(cfg.Printers))
	return nil
}

// PrinterDriver is the unified interface that both fiscal and thermal
// printers must implement.
type PrinterDriver interface {
	Print(job map[string]interface{}) (map[string]interface{}, error)
	Status() (map[string]interface{}, error)
	Kind() string
}

// PrinterPool keeps drivers indexed by printer id.
type PrinterPool struct {
	drivers map[int]PrinterDriver
	mu      sync.RWMutex
}

func NewPrinterPool() *PrinterPool {
	return &PrinterPool{drivers: make(map[int]PrinterDriver)}
}

func (p *PrinterPool) Set(id int, drv PrinterDriver) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drivers[id] = drv
}

func (p *PrinterPool) Get(id int) PrinterDriver {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.drivers[id]
}

func (p *PrinterPool) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drivers = make(map[int]PrinterDriver)
}

func buildPool(printers []PrinterConfig) *PrinterPool {
	pool := NewPrinterPool()
	for _, cfg := range printers {
		drv := buildDriver(cfg)
		if drv != nil {
			pool.Set(cfg.ID, drv)
			log.Printf("Loaded printer #%d: %s (%s/%s)", cfg.ID, cfg.Name, cfg.Kind, cfg.Connection)
		}
	}
	return pool
}

func buildDriver(cfg PrinterConfig) PrinterDriver {
	switch cfg.Kind {
	case "fiscal":
		return &FiscalDriver{inner: NewPrinter(cfg.Brand, cfg.IP, cfg.Port, 15)}
	case "thermal":
		switch cfg.Connection {
		case "lan":
			return NewEscPosLAN(cfg.IP, cfg.Port, cfg.PaperWidthMM, cfg.CharsPerLine, cfg.AutoCut)
		case "usb":
			return NewEscPosUSB(cfg.UsbVendorID, cfg.UsbProductID, cfg.UsbDeviceLabel, cfg.PaperWidthMM, cfg.CharsPerLine, cfg.AutoCut)
		case "bluetooth":
			return NewEscPosBluetooth(cfg.UsbDeviceLabel, cfg.PaperWidthMM, cfg.CharsPerLine, cfg.AutoCut)
		}
	}
	log.Printf("Warning: unsupported printer config: kind=%s connection=%s", cfg.Kind, cfg.Connection)
	return nil
}

// FiscalDriver wraps the legacy FiscalPrinter to implement PrinterDriver.
type FiscalDriver struct {
	inner FiscalPrinter
}

func (d *FiscalDriver) Print(job map[string]interface{}) (map[string]interface{}, error) {
	doc, _ := job["document"].(map[string]interface{})
	if d.inner.HasPendingDocument() {
		log.Println("Cancelling pending fiscal document...")
		d.inner.CancelPending()
	}
	return d.inner.PrintTicket(doc)
}

func (d *FiscalDriver) Status() (map[string]interface{}, error) {
	return d.inner.Status()
}

func (d *FiscalDriver) Kind() string { return "fiscal" }

// ─────────────────────────────────────
// Agent V2 main loop
// ─────────────────────────────────────

func (a *AgentV2) Run() {
	for {
		err := a.connect()
		if err != nil {
			log.Printf("Connection lost: %v", err)
		}
		log.Println("Reconnecting in 5s...")
		time.Sleep(5 * time.Second)
	}
}

func (a *AgentV2) Close() {
	if a.conn != nil {
		a.conn.Close()
	}
}

func (a *AgentV2) connect() error {
	u, err := url.Parse(a.websocketURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	q := u.Query()
	q.Set("agent_token", a.token)
	q.Set("agent_version", version)
	u.RawQuery = q.Encode()

	log.Printf("Connecting to %s...", a.websocketURL)
	// HandshakeTimeout 15s — sin esto, tras un deploy de Rails el dialer
	// puede colgar indefinidamente y el loop de Run() nunca reintenta.
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 15 * time.Second
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	a.conn = conn
	log.Println("Connected to CanchaYa")
	a.subscribe()

	// Discover and report all connected devices (USB + LAN)
	go a.reportDevices()

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
			return fmt.Errorf("connection closed")
		case <-heartbeat.C:
			a.sendAction("heartbeat", nil)
		}
	}
}

// channelIdentifier is the exact JSON used to identify the AgentChannel
// subscription. The same string must be used when subscribing and when sending
// messages — Action Cable indexes subscriptions by this identifier.
const channelIdentifier = `{"channel":"AgentChannel"}`

func (a *AgentV2) subscribe() {
	msg := map[string]string{
		"command":    "subscribe",
		"identifier": channelIdentifier,
	}
	data, _ := json.Marshal(msg)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.conn.WriteMessage(websocket.TextMessage, data)
}

func (a *AgentV2) handleMessage(raw []byte) {
	var msg map[string]interface{}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	msgType, _ := msg["type"].(string)
	switch msgType {
	case "welcome", "ping":
		return
	case "confirm_subscription":
		log.Println("Subscribed to AgentChannel")
		return
	case "reject_subscription":
		log.Println("Subscription rejected")
		return
	}

	message, ok := msg["message"].(map[string]interface{})
	if !ok {
		return
	}

	switch message["type"] {
	case "fiscal.print.request":
		a.handlePrintJob(message, "fiscal")
	case "comanda.print.request":
		a.handlePrintJob(message, "comanda")
	case "test.print.request":
		a.handleTestPrint(message)
	case "agent.reconnect_request":
		// Server pidió que nos desconectemos+reconectemos limpio. Útil cuando
		// el agente está vivo pero medio zombi (procesando lento, WS half-open
		// que nuestro readDeadline tarda en detectar). Cerramos la conexión;
		// el read loop devuelve error → connect() returna → Run() reintenta
		// en 5s con WS fresca.
		log.Printf("Reconnect requested by server (reason=%v)", message["reason"])
		if a.conn != nil {
			a.conn.Close()
		}
	}
}

func (a *AgentV2) handlePrintJob(message map[string]interface{}, jobType string) {
	printerIDFloat, _ := message["printer_id"].(float64)
	printerID := int(printerIDFloat)
	invoiceID := message["invoice_id"]
	jobID := message["job_id"]
	printJobID := message["print_job_id"]
	printerPublicID, _ := message["printer_public_id"].(string)

	// ACK inmediato: avisa al server que recibimos el job antes de imprimir.
	if jobID != nil || printJobID != nil {
		a.sendAction("ack", map[string]interface{}{
			"job_id":       jobID,
			"print_job_id": printJobID,
		})
	}

	driver := a.pool.Get(printerID)
	if driver == nil {
		log.Printf("Impresora #%d desconocida; recargando configuracion...", printerID)
		if err := a.refreshPool(); err != nil {
			log.Printf("Error al recargar configuracion: %v", err)
		}
		driver = a.pool.Get(printerID)
	}
	if driver == nil {
		log.Printf("Impresora #%d sigue sin estar en el pool tras recargar", printerID)
		a.sendAction("print_error", map[string]interface{}{
			"job_type":          jobType,
			"job_id":            jobID,
			"print_job_id":      printJobID,
			"printer_id":        printerID,
			"printer_public_id": printerPublicID,
			"invoice_id":        invoiceID,
			"error_code":        "PRINTER_NOT_FOUND",
			"error_message":     fmt.Sprintf("Impresora #%d no esta configurada en este agente", printerID),
			"retryable":         false,
		})
		return
	}

	log.Printf("Print job: type=%s printer=%d job_id=%v", jobType, printerID, jobID)
	result, err := driver.Print(message)
	if err != nil {
		log.Printf("Print error: %v", err)
		a.sendAction("print_error", map[string]interface{}{
			"job_type":          jobType,
			"job_id":            jobID,
			"print_job_id":      printJobID,
			"printer_id":        printerID,
			"printer_public_id": printerPublicID,
			"invoice_id":        invoiceID,
			"error_code":        "PRINT_ERROR",
			"error_message":     err.Error(),
			"retryable":         true,
		})
		return
	}

	if jobType == "fiscal" {
		fiscalNumber := fmt.Sprintf("%05v-%08v", result["point_of_sale"], result["fiscal_number"])
		log.Printf("Fiscal printed: %s CAE=%v", fiscalNumber, result["cae"])
		a.sendAction("print_result", map[string]interface{}{
			"job_type":          "fiscal",
			"job_id":            jobID,
			"print_job_id":      printJobID,
			"printer_id":        printerID,
			"printer_public_id": printerPublicID,
			"invoice_id":        invoiceID,
			"fiscal_number":     fiscalNumber,
			"printer_response":  result,
		})
	} else {
		log.Printf("Comanda printed on printer #%d", printerID)
		a.sendAction("print_result", map[string]interface{}{
			"job_type":          "comanda",
			"job_id":            jobID,
			"print_job_id":      printJobID,
			"printer_id":        printerID,
			"printer_public_id": printerPublicID,
		})
	}
}

func (a *AgentV2) handleTestPrint(message map[string]interface{}) {
	printerIDFloat, _ := message["printer_id"].(float64)
	printerID := int(printerIDFloat)
	driver := a.pool.Get(printerID)
	if driver == nil {
		// Try refreshing the pool in case the printer was just added.
		if err := a.refreshPool(); err == nil {
			driver = a.pool.Get(printerID)
		}
	}
	if driver == nil {
		a.sendAction("test_result", map[string]interface{}{
			"printer_id":    printerID,
			"success":       false,
			"error_message": "Impresora no configurada en este agente",
		})
		return
	}

	_, err := driver.Print(map[string]interface{}{
		"job_type": "test",
		"comanda": map[string]interface{}{
			"header": map[string]interface{}{"club_name": "Prueba", "time": time.Now().Format("15:04")},
			"items":  []interface{}{map[string]interface{}{"qty": 1, "name": "Test de impresion OK"}},
			"footer": "Prueba de conexion",
		},
	})

	if err != nil {
		a.sendAction("test_result", map[string]interface{}{
			"printer_id":    printerID,
			"success":       false,
			"error_message": err.Error(),
		})
	} else {
		a.sendAction("test_result", map[string]interface{}{
			"printer_id": printerID,
			"success":    true,
		})
	}
}

func (a *AgentV2) reportDevices() {
	time.Sleep(1 * time.Second)

	scanDone := make(chan struct{})
	go func() {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		for {
			select {
			case <-scanDone:
				fmt.Print("\r\033[K")
				return
			default:
				fmt.Printf("\r%s Buscando impresoras (USB + red + Bluetooth)...", frames[i%len(frames)])
				i++
				time.Sleep(120 * time.Millisecond)
			}
		}
	}()

	usbDevices, err := DiscoverUSBDevices()
	if err != nil {
		log.Printf("Error buscando USB: %v", err)
	}

	lanDevices := ScanLANPrinters()
	btDevices := DiscoverBluetoothDevices()

	close(scanDone)
	time.Sleep(50 * time.Millisecond)

	allDevices := make([]map[string]interface{}, 0, len(usbDevices)+len(lanDevices)+len(btDevices))
	allDevices = append(allDevices, usbDevices...)
	allDevices = append(allDevices, btDevices...)

	for _, ld := range lanDevices {
		allDevices = append(allDevices, map[string]interface{}{
			"vendor_id":    "",
			"product_id":   "",
			"label":        ld.Label,
			"manufacturer": ld.Brand,
			"product":      ld.Label,
			"serial":       "",
			"connection":   "lan",
			"ip":           ld.IP,
			"port":         ld.Port,
			"kind":         ld.Kind,
		})
	}

	if len(allDevices) == 0 {
		log.Println("No se encontraron impresoras")
	} else {
		for _, d := range allDevices {
			conn := "USB"
			switch d["connection"] {
			case "lan":
				conn = "LAN"
			case "bluetooth":
				conn = "BT"
			}
			log.Printf("  %s: %s", conn, d["label"])
		}
	}

	a.sendAction("devices_discovered", map[string]interface{}{
		"devices": allDevices,
	})
}

func (a *AgentV2) sendAction(action string, data map[string]interface{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.conn == nil {
		return
	}
	payload := map[string]interface{}{"action": action}
	for k, v := range data {
		payload[k] = v
	}
	payloadJSON, _ := json.Marshal(payload)
	msg := map[string]string{
		"command":    "message",
		"identifier": channelIdentifier,
		"data":       string(payloadJSON),
	}
	msgJSON, _ := json.Marshal(msg)
	a.conn.WriteMessage(websocket.TextMessage, msgJSON)
}
