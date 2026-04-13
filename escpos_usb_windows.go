//go:build windows

package main

import (
	"fmt"
	"log"

	winprint "github.com/alexbrainman/printer"
)

// On Windows we use the OS Print Spooler instead of libusb. The user installs
// the thermal printer with its driver (or the generic 'Generic / Text Only')
// and Windows exposes it by name. We send raw ESC/POS bytes via WritePrinter.
//
// The schema we share with the server uses usb_vendor_id / usb_product_id /
// usb_device_label. On Windows, vendor/product are unused — usb_device_label
// holds the Windows printer name (e.g. "Global TP-POS80").
type EscPosUSB struct {
	printerName  string
	paperWidthMM int
	charsPerLine int
	autoCut      bool
}

// NewEscPosUSB on Windows accepts the printer name in usbDeviceLabel.
// vendorID/productID are ignored.
func NewEscPosUSB(vendorID, productID, usbDeviceLabel string, paperWidthMM, charsPerLine int, autoCut bool) *EscPosUSB {
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
	pr, err := winprint.Open(p.printerName)
	if err != nil {
		return map[string]interface{}{"online": false, "error": err.Error()}, nil
	}
	pr.Close()
	return map[string]interface{}{"online": true}, nil
}

func (p *EscPosUSB) Print(job map[string]interface{}) (map[string]interface{}, error) {
	if p.printerName == "" {
		return nil, fmt.Errorf("printer name not configured")
	}

	pr, err := winprint.Open(p.printerName)
	if err != nil {
		return nil, fmt.Errorf("open printer %q: %w", p.printerName, err)
	}
	defer pr.Close()

	if err := pr.StartRawDocument("Mi Tienda comanda"); err != nil {
		return nil, fmt.Errorf("start raw document: %w", err)
	}
	defer pr.EndDocument()

	if err := pr.StartPage(); err != nil {
		return nil, fmt.Errorf("start page: %w", err)
	}

	bytes := buildEscPosBytes(job, p.charsPerLine, p.autoCut)
	written, err := pr.Write(bytes)
	if err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	if err := pr.EndPage(); err != nil {
		return nil, fmt.Errorf("end page: %w", err)
	}

	return map[string]interface{}{"printed": true, "bytes": written}, nil
}

// DiscoverUSBDevices on Windows enumerates installed printers via the spooler.
// We expose them with vendor_id/product_id empty and label = printer name so
// the server-side flow (matching by label) still works.
func DiscoverUSBDevices() ([]map[string]interface{}, error) {
	names, err := winprint.ReadNames()
	if err != nil {
		log.Printf("ReadNames: %v", err)
		return []map[string]interface{}{}, nil
	}

	devices := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		// Skip obvious non-printer entries that always show up on Windows
		switch name {
		case "Microsoft Print to PDF",
			"Microsoft XPS Document Writer",
			"OneNote (Desktop)",
			"Fax",
			"Send To OneNote 2016":
			continue
		}
		devices = append(devices, map[string]interface{}{
			"vendor_id":    "",
			"product_id":   "",
			"label":        name,
			"manufacturer": "",
			"product":      name,
			"serial":       "",
		})
	}
	return devices, nil
}
