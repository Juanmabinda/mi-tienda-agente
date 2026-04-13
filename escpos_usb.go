//go:build (darwin || linux) && !nousb

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
)

// Minimal PPD that tells CUPS to pass data through unchanged.
// Modern macOS (13+) rejects -m raw, so we create a proper PPD with a
// raw passthrough filter instead.
const rawPPD = `*PPD-Adobe: "4.3"
*FormatVersion: "4.3"
*FileVersion: "1.0"
*LanguageVersion: English
*LanguageEncoding: ISOLatin1
*Manufacturer: "Generic"
*ModelName: "Mi Tienda Raw Printer"
*ShortNickName: "Mi Tienda Raw"
*NickName: "Mi Tienda Raw Printer"
*cupsFilter: "application/vnd.cups-raw 0 -"
*OpenUI *PageSize/Page Size: PickOne
*DefaultPageSize: X80MMY297MM
*PageSize X80MMY297MM/80mm Roll: ""
*CloseUI: *PageSize
*OpenUI *PageRegion: PickOne
*DefaultPageRegion: X80MMY297MM
*PageRegion X80MMY297MM/80mm Roll: ""
*CloseUI: *PageRegion
*DefaultImageableArea: X80MMY297MM
*ImageableArea X80MMY297MM/80mm Roll: "0 0 226 841"
*DefaultPaperDimension: X80MMY297MM
*PaperDimension X80MMY297MM/80mm Roll: "226 841"
`

// On Mac/Linux we use CUPS. The agent auto-creates raw CUPS queues for any
// USB printer it finds via lpinfo, then prints via lp. No user interaction
// needed — no driver selection, no System Settings.

type EscPosUSB struct {
	printerName  string
	paperWidthMM int
	charsPerLine int
	autoCut      bool
}

func NewEscPosUSB(vendorID, productID, usbDeviceLabel string, paperWidthMM, charsPerLine int, autoCut bool) *EscPosUSB {
	_ = vendorID
	_ = productID
	if charsPerLine == 0 {
		if paperWidthMM == 58 {
			charsPerLine = 32
		} else {
			charsPerLine = 48
		}
	}
	return &EscPosUSB{
		printerName:  usbDeviceLabel,
		paperWidthMM: paperWidthMM,
		charsPerLine: charsPerLine,
		autoCut:      autoCut,
	}
}

func (p *EscPosUSB) Kind() string { return "thermal" }

func (p *EscPosUSB) Status() (map[string]interface{}, error) {
	if p.printerName == "" {
		return map[string]interface{}{"online": false, "error": "printer name not set"}, nil
	}
	out, err := exec.Command("lpstat", "-p", p.printerName).Output()
	if err != nil {
		return map[string]interface{}{"online": false, "error": err.Error()}, nil
	}
	return map[string]interface{}{"online": true, "lpstat": strings.TrimSpace(string(out))}, nil
}

func (p *EscPosUSB) Print(job map[string]interface{}) (map[string]interface{}, error) {
	if p.printerName == "" {
		return nil, fmt.Errorf("nombre de impresora no configurado")
	}

	data := buildEscPosBytes(job, p.charsPerLine, p.autoCut)

	// Write to a temp file — piping via stdin to lp can lose initial bytes
	// on macOS due to CUPS buffering/signal handling.
	tmpFile, err := os.CreateTemp("", "comanda-*.bin")
	if err != nil {
		return nil, fmt.Errorf("temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("write temp: %w", err)
	}
	tmpFile.Close()

	out, err := exec.Command("lp", "-d", p.printerName, "-o", "raw", tmpPath).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("lp: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	return map[string]interface{}{
		"printed":     true,
		"bytes":       len(data),
		"lp_response": strings.TrimSpace(string(out)),
	}, nil
}

// DiscoverUSBDevices finds USB printers via lpinfo and auto-creates CUPS
// queues (raw mode, no driver) for any that don't have one yet. Returns the
// list of CUPS printer names ready for use.
func DiscoverUSBDevices() ([]map[string]interface{}, error) {
	// 1. Find USB printer URIs via lpinfo
	usbURIs := discoverUSBURIs()

	// 2. Get already-configured CUPS queues and their device URIs
	existingQueues := cupsQueuesByURI()

	// 3. Auto-create missing queues
	ppdPath := ensureRawPPD()
	for _, info := range usbURIs {
		uri := info["uri"]
		if _, exists := existingQueues[uri]; !exists {
			name := sanitizeCupsName(info["label"])
			if name == "" {
				name = "Mi Tienda-Printer"
			}
			// Ensure name is unique
			base := name
			for i := 2; existingQueues[name] != ""; i++ {
				name = fmt.Sprintf("%s-%d", base, i)
			}
			log.Printf("Auto-creating CUPS queue %q for %s", name, uri)
			// Try with PPD first (macOS 13+ rejects -m raw), fall back to -m raw for Linux
			args := []string{"-p", name, "-E", "-v", uri}
			if ppdPath != "" {
				args = append(args, "-P", ppdPath)
			} else {
				args = append(args, "-m", "raw")
			}
			out, err := exec.Command("lpadmin", args...).CombinedOutput()
			if err != nil {
				log.Printf("lpadmin error: %v (%s)", err, strings.TrimSpace(string(out)))
				continue
			}
			existingQueues[uri] = name
			existingQueues[name] = uri // reverse lookup
		}
	}

	// 4. Return all CUPS printer queues (not just USB — user might have LAN too)
	devices := []map[string]interface{}{}
	seen := map[string]bool{}
	for _, info := range usbURIs {
		uri := info["uri"]
		queueName := existingQueues[uri]
		if queueName == "" || seen[queueName] {
			continue
		}
		seen[queueName] = true
		devices = append(devices, map[string]interface{}{
			"vendor_id":    "",
			"product_id":   "",
			"label":        queueName,
			"manufacturer": "",
			"product":      info["label"],
			"serial":       "",
		})
	}
	return devices, nil
}

// discoverUSBURIs runs lpinfo -v and returns USB device URIs with labels.
func discoverUSBURIs() []map[string]string {
	out, err := exec.Command("lpinfo", "-v").Output()
	if err != nil {
		log.Printf("lpinfo: %v", err)
		return nil
	}

	var results []map[string]string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// Lines look like: "direct usb://0x1fc9/0x2016?serial=ABC" or "direct usb://Brand/Model?serial=..."
		if !strings.Contains(line, "usb://") {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		uri := strings.TrimSpace(parts[1])
		label := extractLabelFromURI(uri)
		results = append(results, map[string]string{"uri": uri, "label": label})
	}
	return results
}

// cupsQueuesByURI returns a map of device-uri → queue-name for all configured queues.
func cupsQueuesByURI() map[string]string {
	out, err := exec.Command("lpstat", "-v").Output()
	if err != nil {
		return map[string]string{}
	}

	queues := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		// Lines: "device for QueueName: usb://..."
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "device for ") {
			continue
		}
		rest := strings.TrimPrefix(line, "device for ")
		idx := strings.Index(rest, ":")
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(rest[:idx])
		uri := strings.TrimSpace(rest[idx+1:])
		queues[uri] = name
		queues[name] = uri // reverse
	}
	return queues
}

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func sanitizeCupsName(label string) string {
	name := nonAlphanumeric.ReplaceAllString(label, "-")
	name = strings.Trim(name, "-")
	if len(name) > 40 {
		name = name[:40]
	}
	return name
}

// ensureRawPPD writes the passthrough PPD to the agent's data directory and
// returns its path. Returns "" if writing fails (Linux can use -m raw instead).
func ensureRawPPD() string {
	var dir string
	if home, err := os.UserHomeDir(); err == nil {
		switch runtime.GOOS {
		case "darwin":
			dir = filepath.Join(home, "Library", "Application Support", "Mi Tienda Print")
		default:
			dir = filepath.Join(home, ".config", "mitienda-print")
		}
	}
	if dir == "" {
		return ""
	}
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "raw-printer.ppd")
	if err := os.WriteFile(path, []byte(rawPPD), 0o644); err != nil {
		log.Printf("Warning: could not write PPD to %s: %v", path, err)
		return ""
	}
	return path
}

func extractLabelFromURI(uri string) string {
	// uri: "usb://Brand/Model?serial=..." → "Brand Model"
	u := strings.TrimPrefix(uri, "usb://")
	if idx := strings.Index(u, "?"); idx >= 0 {
		u = u[:idx]
	}
	parts := strings.SplitN(u, "/", 2)
	if len(parts) == 2 {
		brand := strings.ReplaceAll(parts[0], "%20", " ")
		model := strings.ReplaceAll(parts[1], "%20", " ")
		if brand == model || brand == "Unknown" || brand == "" {
			return model
		}
		return brand + " " + model
	}
	return u
}
