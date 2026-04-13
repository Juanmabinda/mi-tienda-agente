//go:build !windows && nousb

package main

import (
	"fmt"
	"log"
)

// EscPosUSB stub when built with -tags nousb (no libusb dependency).
type EscPosUSB struct {
	vendorID     string
	productID    string
	paperWidthMM int
	charsPerLine int
	autoCut      bool
}

func NewEscPosUSB(vendorID, productID, usbDeviceLabel string, paperWidthMM, charsPerLine int, autoCut bool) *EscPosUSB {
	_ = usbDeviceLabel
	log.Printf("Warning: USB driver disabled in this build (vendor=%s product=%s)", vendorID, productID)
	return &EscPosUSB{
		vendorID:     vendorID,
		productID:    productID,
		paperWidthMM: paperWidthMM,
		charsPerLine: charsPerLine,
		autoCut:      autoCut,
	}
}

func (p *EscPosUSB) Kind() string { return "thermal" }

func (p *EscPosUSB) Status() (map[string]interface{}, error) {
	return map[string]interface{}{"online": false, "error": "USB disabled in build"}, nil
}

func (p *EscPosUSB) Print(job map[string]interface{}) (map[string]interface{}, error) {
	return nil, fmt.Errorf("USB printing disabled in this build")
}

func DiscoverUSBDevices() ([]map[string]interface{}, error) {
	return []map[string]interface{}{}, nil
}
