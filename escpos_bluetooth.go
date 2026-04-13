package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EscPosBluetooth prints via Bluetooth serial port (macOS: /dev/tty.*, Linux: /dev/rfcomm*)
type EscPosBluetooth struct {
	devicePath   string
	paperWidthMM int
	charsPerLine int
	autoCut      bool
}

func NewEscPosBluetooth(devicePath string, paperWidthMM, charsPerLine int, autoCut bool) *EscPosBluetooth {
	if charsPerLine == 0 {
		if paperWidthMM == 58 {
			charsPerLine = 32
		} else {
			charsPerLine = 48
		}
	}
	return &EscPosBluetooth{
		devicePath:   devicePath,
		paperWidthMM: paperWidthMM,
		charsPerLine: charsPerLine,
		autoCut:      autoCut,
	}
}

func (p *EscPosBluetooth) Kind() string { return "thermal" }

func (p *EscPosBluetooth) Status() (map[string]interface{}, error) {
	_, err := os.Stat(p.devicePath)
	if err != nil {
		return map[string]interface{}{"online": false, "error": "device not found: " + p.devicePath}, nil
	}
	return map[string]interface{}{"online": true, "device": p.devicePath}, nil
}

func (p *EscPosBluetooth) Print(job map[string]interface{}) (map[string]interface{}, error) {
	data := buildEscPosBytes(job, p.charsPerLine, p.autoCut)

	f, err := os.OpenFile(p.devicePath, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open bluetooth device %s: %w", p.devicePath, err)
	}
	defer f.Close()

	// Send in chunks with small delays (BT serial is slower than LAN)
	chunkSize := 128
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		if _, err := f.Write(data[i:end]); err != nil {
			return nil, fmt.Errorf("write failed: %w", err)
		}
		if end < len(data) {
			time.Sleep(50 * time.Millisecond)
		}
	}

	return map[string]interface{}{"printed": true}, nil
}

// DiscoverBluetoothDevices finds Bluetooth serial devices that look like thermal printers.
// On macOS these appear as /dev/tty.* (excluding system devices).
// On Linux they appear as /dev/rfcomm*.
func DiscoverBluetoothDevices() []map[string]interface{} {
	var devices []map[string]interface{}

	// macOS: /dev/tty.*
	matches, _ := filepath.Glob("/dev/tty.*")
	for _, path := range matches {
		name := filepath.Base(path)
		// Skip system Bluetooth devices
		if strings.Contains(name, "Bluetooth-Incoming") ||
			strings.Contains(name, "debug-console") ||
			strings.Contains(name, "BLTH") ||
			strings.Contains(name, "wlan") {
			continue
		}
		// Likely a thermal printer
		log.Printf("  BT: %s", name)
		devices = append(devices, map[string]interface{}{
			"vendor_id":    "",
			"product_id":   "",
			"label":        name,
			"manufacturer": "Bluetooth",
			"product":      name,
			"serial":       "",
			"connection":   "bluetooth",
			"device_path":  path,
			"kind":         "thermal",
		})
	}

	// Linux: /dev/rfcomm*
	rfcommMatches, _ := filepath.Glob("/dev/rfcomm*")
	for _, path := range rfcommMatches {
		name := filepath.Base(path)
		log.Printf("  BT: %s", name)
		devices = append(devices, map[string]interface{}{
			"vendor_id":    "",
			"product_id":   "",
			"label":        name,
			"manufacturer": "Bluetooth",
			"product":      name,
			"serial":       "",
			"connection":   "bluetooth",
			"device_path":  path,
			"kind":         "thermal",
		})
	}

	return devices
}
